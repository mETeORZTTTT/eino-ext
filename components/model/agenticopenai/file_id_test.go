/*
 * Copyright 2026 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     https://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package agenticopenai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/require"
)

func TestResponsesFileIDInputEncoding(t *testing.T) {
	image, err := userInputImageToInputItem(responses.EasyInputMessageRoleUser, &schema.UserInputImage{FileID: "file-image", Detail: schema.ImageURLDetailHigh})
	require.NoError(t, err)
	raw, err := json.Marshal(image.OfMessage.Content.OfInputItemContentList[0])
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"input_image","file_id":"file-image","detail":"high"}`, string(raw))
	file, err := userInputFileToInputItem(responses.EasyInputMessageRoleUser, &schema.UserInputFile{FileID: "file-document", Name: "local.pdf"})
	require.NoError(t, err)
	raw, err = json.Marshal(file.OfMessage.Content.OfInputItemContentList[0])
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"input_file","file_id":"file-document"}`, string(raw))
}

func TestResponsesFileIDInputConflicts(t *testing.T) {
	for _, source := range []struct{ id, url, data string }{{"file-example", "https://example.com/a", ""}, {"file-example", "", "AAAA"}, {" ", "", ""}} {
		_, err := userInputImageToInputItem(responses.EasyInputMessageRoleUser, &schema.UserInputImage{FileID: source.id, URL: source.url, Base64Data: source.data})
		require.Error(t, err)
		_, err = userInputFileToInputItem(responses.EasyInputMessageRoleUser, &schema.UserInputFile{FileID: source.id, URL: source.url, Base64Data: source.data})
		require.Error(t, err)
	}
}

func TestResponsesFileIDDoesNotChangeURL(t *testing.T) {
	image, err := userInputImageToInputItem(responses.EasyInputMessageRoleUser, &schema.UserInputImage{URL: "https://example.com/image.png", Detail: schema.ImageURLDetailHigh})
	require.NoError(t, err)
	raw, err := json.Marshal(image.OfMessage.Content.OfInputItemContentList[0])
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"input_image","image_url":"https://example.com/image.png","detail":"high"}`, string(raw))
}

// Exercise the public Generate and Stream entry points with the real SDK encoder.
// The local server also verifies that a provider file-expiry error is preserved.
func TestFileIDGenerateAndStreamRequests(t *testing.T) {
	var inputs []any
	for _, stream := range []bool{false, true} {
		ctx := context.Background()
		bodies := make(chan []byte, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			bodies <- body
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"code":"InvalidParameter","type":"invalid_request_error","message":"file expired; upload again"}}`)
		}))
		model, err := NewResponsesModel(ctx, &ResponsesConfig{APIKey: "test-key", Model: "test-model", BaseURL: server.URL})
		require.NoError(t, err)
		messages := []*schema.AgenticMessage{{Role: schema.AgenticRoleTypeUser, ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.UserInputImage{FileID: "file-image", Detail: schema.ImageURLDetailAuto}),
			schema.NewContentBlock(&schema.UserInputFile{FileID: "file-document"}),
		}}}
		// Execute a real enhanced tool through the core ToolsNode before encoding
		// its file-backed result in the provider request.
		node, err := compose.NewAgenticToolsNode(ctx, &compose.ToolsNodeConfig{Tools: []tool.BaseTool{&fileIDResultTool{}}})
		require.NoError(t, err)
		call := &schema.AgenticMessage{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{CallID: "call-media", Name: "read_media", Arguments: "{}"}),
		}}
		toolMessages, err := node.Invoke(ctx, call)
		require.NoError(t, err)
		messages = append(messages, call)
		messages = append(messages, toolMessages...)
		if stream {
			_, err = model.Stream(ctx, messages)
		} else {
			_, err = model.Generate(ctx, messages)
		}
		server.Close()
		require.ErrorContains(t, err, "file expired")
		var body map[string]any
		select {
		case raw := <-bodies:
			require.NoError(t, json.Unmarshal(raw, &body))
			require.Contains(t, string(raw), `"file_id":"file-image"`)
			require.Contains(t, string(raw), `"file_id":"file-document"`)
			require.Contains(t, string(raw), `"file_id":"file-tool-image"`)
			require.Contains(t, string(raw), `"file_id":"file-tool-document"`)
			var toolOutput map[string]any
			for _, input := range body["input"].([]any) {
				item := input.(map[string]any)
				if item["type"] == "function_call_output" {
					toolOutput = item
				}
			}
			require.NotNil(t, toolOutput)
			require.Equal(t, "call-media", toolOutput["call_id"])
			require.Equal(t, []any{
				map[string]any{"type": "input_image", "file_id": "file-tool-image"},
				map[string]any{"type": "input_file", "file_id": "file-tool-document"},
			}, toolOutput["output"])
			require.NotContains(t, string(raw), `"image_url"`)
			require.NotContains(t, string(raw), `"video_url"`)
			require.NotContains(t, string(raw), `"file_url"`)
		default:
			t.Fatal("no request reached the test server")
		}
		inputs = append(inputs, body["input"])
	}
	require.Equal(t, inputs[0], inputs[1], "Generate and Stream must encode the same file references")
}

// Only the tool's external media acquisition is replaced. Core result conversion
// and provider request construction run normally.
type fileIDResultTool struct{}

func (*fileIDResultTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "read_media", Desc: "Return uploaded media references"}, nil
}

func (*fileIDResultTool) InvokableRun(context.Context, *schema.ToolArgument, ...tool.Option) (*schema.ToolResult, error) {
	imageID, fileID := "file-tool-image", "file-tool-document"
	return &schema.ToolResult{Parts: []schema.ToolOutputPart{
		{Type: schema.ToolPartTypeImage, Image: &schema.ToolOutputImage{MessagePartCommon: schema.MessagePartCommon{FileID: &imageID}}},
		{Type: schema.ToolPartTypeFile, File: &schema.ToolOutputFile{MessagePartCommon: schema.MessagePartCommon{FileID: &fileID}}},
	}}, nil
}

func TestResponsesToolResultFileIDEncoding(t *testing.T) {
	for _, tc := range []struct {
		name     string
		block    *schema.FunctionToolResultContentBlock
		expected string
	}{
		{"image_file_id", &schema.FunctionToolResultContentBlock{Type: schema.FunctionToolResultContentBlockTypeImage, Image: &schema.UserInputImage{FileID: "file-image", Detail: schema.ImageURLDetailHigh}}, `{"type":"input_image","file_id":"file-image","detail":"high"}`},
		{"document_file_id", &schema.FunctionToolResultContentBlock{Type: schema.FunctionToolResultContentBlockTypeFile, File: &schema.UserInputFile{FileID: "file-document", Name: "ignored.pdf"}}, `{"type":"input_file","file_id":"file-document"}`},
		{"image_url", &schema.FunctionToolResultContentBlock{Type: schema.FunctionToolResultContentBlockTypeImage, Image: &schema.UserInputImage{URL: "https://example.com/a.png"}}, `{"type":"input_image","image_url":"https://example.com/a.png"}`},
		{"document_url", &schema.FunctionToolResultContentBlock{Type: schema.FunctionToolResultContentBlockTypeFile, File: &schema.UserInputFile{URL: "https://example.com/a.pdf", Name: "a.pdf"}}, `{"type":"input_file","file_url":"https://example.com/a.pdf","filename":"a.pdf"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, err := functionToolResultContentToOutput([]*schema.FunctionToolResultContentBlock{tc.block})
			require.NoError(t, err)
			raw, err := json.Marshal(output)
			require.NoError(t, err)
			require.JSONEq(t, "["+tc.expected+"]", string(raw))
		})
	}
}

func TestResponsesToolResultFileIDConflicts(t *testing.T) {
	for _, source := range []struct{ id, url, data string }{
		{"file-example", "https://example.com/a", ""},
		{"file-example", "", "AAAA"},
		{" ", "", ""},
	} {
		for _, block := range []*schema.FunctionToolResultContentBlock{
			{Type: schema.FunctionToolResultContentBlockTypeImage, Image: &schema.UserInputImage{FileID: source.id, URL: source.url, Base64Data: source.data}},
			{Type: schema.FunctionToolResultContentBlockTypeFile, File: &schema.UserInputFile{FileID: source.id, URL: source.url, Base64Data: source.data}},
		} {
			_, err := functionToolResultContentToOutput([]*schema.FunctionToolResultContentBlock{block})
			require.Error(t, err)
		}
	}
}

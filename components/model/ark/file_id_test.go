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

package ark

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func TestResponsesFileIDEncoding(t *testing.T) {
	for _, kind := range []string{"image", "video", "audio", "file"} {
		t.Run(kind, func(t *testing.T) {
			id := "file-example"
			common := schema.MessagePartCommon{FileID: &id}
			var item *responses.ContentItem
			var err error
			switch kind {
			case "image":
				item, err = convMsgInputImageToResponseContentItem(&schema.MessageInputImage{MessagePartCommon: common, Detail: schema.ImageURLDetailHigh})
			case "video":
				item, err = convMsgInputVideoToResponseContentItem(&schema.MessageInputVideo{MessagePartCommon: common})
			case "audio":
				item, err = convMsgInputAudioToResponseContentItem(&schema.MessageInputAudio{MessagePartCommon: common})
			case "file":
				item, err = convMsgInputFileToResponseContentItem(&schema.MessageInputFile{MessagePartCommon: common, Name: "local-name.pdf"})
			}
			require.NoError(t, err)
			raw, err := json.Marshal(item)
			require.NoError(t, err)
			expected := `{"type":"input_` + kind + `","file_id":"file-example"}`
			if kind == "image" {
				expected = `{"type":"input_image","file_id":"file-example","detail":"high"}`
			}
			require.JSONEq(t, expected, string(raw))
		})
	}
}

func TestResponsesFileIDConflicts(t *testing.T) {
	id, empty, url, data := "file-example", "", "https://example.com/a", "AAAA"
	for _, common := range []schema.MessagePartCommon{
		{FileID: &empty}, {FileID: &id, URL: &url}, {FileID: &id, Base64Data: &data}, {FileID: &id, URL: &empty},
	} {
		_, err := convMsgInputImageToResponseContentItem(&schema.MessageInputImage{MessagePartCommon: common})
		require.Error(t, err)
		_, err = convMsgInputVideoToResponseContentItem(&schema.MessageInputVideo{MessagePartCommon: common})
		require.Error(t, err)
		_, err = convMsgInputAudioToResponseContentItem(&schema.MessageInputAudio{MessagePartCommon: common})
		require.Error(t, err)
		_, err = convMsgInputFileToResponseContentItem(&schema.MessageInputFile{MessagePartCommon: common})
		require.Error(t, err)
	}
	_, _, err := validateMessagePartCommon(schema.MessagePartCommon{FileID: &id, URL: &url}, "image")
	require.ErrorContains(t, err, "not supported by the current Ark Chat SDK")
}

func TestResponsesFileIDDoesNotChangeURL(t *testing.T) {
	url := "https://example.com/video.mp4"
	item, err := convMsgInputVideoToResponseContentItem(&schema.MessageInputVideo{MessagePartCommon: schema.MessagePartCommon{URL: &url}})
	require.NoError(t, err)
	raw, err := json.Marshal(item)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"input_video","video_url":"https://example.com/video.mp4"}`, string(raw))
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
		model, err := NewResponsesAPIChatModel(ctx, &ResponsesAPIConfig{APIKey: "test-key", Model: "test-model", BaseURL: server.URL})
		require.NoError(t, err)
		id := "file-image"
		videoID := "file-video"
		messages := []*schema.Message{{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{
			{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{FileID: &id}, Detail: schema.ImageURLDetailAuto}},
			{Type: schema.ChatMessagePartTypeVideoURL, Video: &schema.MessageInputVideo{MessagePartCommon: schema.MessagePartCommon{FileID: &videoID}}},
		}}}
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
			require.Contains(t, string(raw), `"file_id":"file-video"`)
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

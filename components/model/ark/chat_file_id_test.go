/*
 * Copyright 2024 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	sdk "github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
)

func chatFileInput(kind string, role schema.RoleType, common schema.MessagePartCommon) *schema.Message {
	part := schema.MessageInputPart{}
	if kind == "image" {
		part.Type = schema.ChatMessagePartTypeImageURL
		part.Image = &schema.MessageInputImage{MessagePartCommon: common, Detail: "high"}
	} else {
		part.Type = schema.ChatMessagePartTypeVideoURL
		part.Video = &schema.MessageInputVideo{MessagePartCommon: common}
		SetInputVideoFPS(part.Video, 0.5)
	}
	msg := &schema.Message{Role: role, UserInputMultiContent: []schema.MessageInputPart{part}}
	if role == schema.Tool {
		msg.ToolCallID = "call_fixture"
	}
	return msg
}

func TestChatMediaFileIDEncoding(t *testing.T) {
	cm := &completionAPIChatModel{}
	for _, kind := range []string{"image", "video"} {
		for _, role := range []schema.RoleType{schema.User, schema.Tool} {
			t.Run(kind+"/"+string(role), func(t *testing.T) {
				id := "file-fixture"
				msg := chatFileInput(kind, role, schema.MessagePartCommon{FileID: &id})
				got, err := cm.toArkContent(msg)
				if err != nil {
					t.Fatal(err)
				}
				data, err := json.Marshal(got)
				if err != nil {
					t.Fatal(err)
				}
				var parts []map[string]json.RawMessage
				if err := json.Unmarshal(data, &parts); err != nil {
					t.Fatal(err)
				}
				var media map[string]any
				if err := json.Unmarshal(parts[0][kind+"_url"], &media); err != nil {
					t.Fatal(err)
				}
				if media["file_id"] != id {
					t.Fatalf("missing file_id: %s", data)
				}
				if _, ok := media["url"]; ok {
					t.Fatalf("file input contains URL: %s", data)
				}
				if kind == "image" && media["detail"] != "high" {
					t.Fatalf("lost detail: %s", data)
				}
				if kind == "video" && media["fps"] != 0.5 {
					t.Fatalf("lost fps: %s", data)
				}
				id = ""
				if _, err := cm.toArkContent(msg); err == nil {
					t.Fatal("empty file_id accepted")
				}
			})
		}
		t.Run(kind+"/source_precedence", func(t *testing.T) {
			checkFileIDPrecedence(t, func(common schema.MessagePartCommon) (any, error) {
				return cm.toArkContent(chatFileInput(kind, schema.User, common))
			})
		})
	}
}

const chatFileUsage = `"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_tokens_details":{"cached_tokens":30},"completion_tokens_details":{"reasoning_tokens":5}}`

func TestChatMediaFileIDWire(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			received := make(chan map[string]json.RawMessage, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/chat/completions" {
					t.Errorf("unexpected API path %s", r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer fixture" {
					t.Error("authorization missing")
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				received <- body
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, `data: {"id":"chat_fixture","model":"fixture","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`+"\n\n")
					fmt.Fprint(w, `data: {"id":"chat_fixture","model":"fixture","choices":[],`+chatFileUsage+"}\n\ndata: [DONE]\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"id":"chat_fixture","model":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+chatFileUsage+`}`)
				}
			}))
			defer srv.Close()
			zero := 0
			cm, err := NewChatModel(context.Background(), &ChatModelConfig{APIKey: "fixture", BaseURL: srv.URL, Model: "fixture", RetryTimes: &zero})
			if err != nil {
				t.Fatal(err)
			}
			id := "file-image"
			videoID := "file-video"
			input := []*schema.Message{
				chatFileInput("image", schema.User, schema.MessagePartCommon{FileID: &id}),
				{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "call_fixture", Type: "function", Function: schema.FunctionCall{Name: "read_media", Arguments: `{}`}}}},
				chatFileInput("video", schema.Tool, schema.MessagePartCommon{FileID: &videoID}),
			}
			var output *schema.Message
			if stream {
				sr, e := cm.Stream(context.Background(), input)
				if e != nil {
					t.Fatal(e)
				}
				defer sr.Close()
				var chunks []*schema.Message
				for {
					chunk, e := sr.Recv()
					if errors.Is(e, io.EOF) {
						break
					}
					if e != nil {
						t.Fatal(e)
					}
					chunks = append(chunks, chunk)
				}
				output, err = schema.ConcatMessages(chunks)
			} else {
				output, err = cm.Generate(context.Background(), input)
			}
			if err != nil {
				t.Fatal(err)
			}
			if output.Content != "ok" {
				t.Fatalf("output=%+v", output)
			}
			u := output.ResponseMeta.Usage
			if u == nil || u.PromptTokens != 100 || u.CompletionTokens != 20 || u.TotalTokens != 120 || u.PromptTokenDetails.CachedTokens != 30 || u.CompletionTokensDetails.ReasoningTokens != 5 {
				t.Fatalf("usage=%+v", u)
			}
			body := <-received
			var msgs []struct {
				Role      string          `json:"role"`
				CallID    string          `json:"tool_call_id"`
				Content   json.RawMessage `json:"content"`
				ToolCalls json.RawMessage `json:"tool_calls"`
			}
			if err := json.Unmarshal(body["messages"], &msgs); err != nil {
				t.Fatal(err)
			}
			if len(msgs) != 3 || msgs[2].Role != "tool" || msgs[2].CallID != "call_fixture" || !strings.Contains(string(msgs[1].ToolCalls), "read_media") {
				t.Fatalf("lost tool history: %s", body["messages"])
			}
			for i, want := range map[int]string{0: id, 2: videoID} {
				if !strings.Contains(string(msgs[i].Content), `"file_id":"`+want+`"`) || strings.Contains(string(msgs[i].Content), `"url":`) {
					t.Fatalf("bad file part: %s", msgs[i].Content)
				}
			}
		})
	}
}

func TestChatMediaFileIDPreservesAPIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"code":"InvalidParameter","message":"file is expired","type":"BadRequest","param":"messages.content.image_url.file_id"}}`)
	}))
	defer srv.Close()
	zero := 0
	cm, err := NewChatModel(context.Background(), &ChatModelConfig{APIKey: "fixture", BaseURL: srv.URL, Model: "fixture", RetryTimes: &zero})
	if err != nil {
		t.Fatal(err)
	}
	id := "file-fixture"
	in := []*schema.Message{chatFileInput("image", schema.User, schema.MessagePartCommon{FileID: &id})}
	for _, stream := range []bool{false, true} {
		if stream {
			_, err = cm.Stream(context.Background(), in)
		} else {
			_, err = cm.Generate(context.Background(), in)
		}
		var api *sdk.APIError
		if !errors.As(err, &api) || api.HTTPStatusCode != 400 || api.Code != "InvalidParameter" {
			t.Fatalf("lost SDK error: %T %v", err, err)
		}
	}
}

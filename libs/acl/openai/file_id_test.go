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

package openai

import (
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

func TestFileIDBridgeAndChatRejection(t *testing.T) {
	image := imageBlockToInputPart(&schema.UserInputImage{FileID: "file-image", URL: "https://example.com/image"}, nil)
	audio := audioBlockToInputPart(&schema.UserInputAudio{FileID: "file-audio"}, nil)
	video := videoBlockToInputPart(&schema.UserInputVideo{FileID: "file-video"}, nil)
	require.Equal(t, "file-image", *image.Image.FileID)
	require.Equal(t, "https://example.com/image", *image.Image.URL)
	require.Equal(t, "file-audio", *audio.Audio.FileID)
	require.Equal(t, "file-video", *video.Video.FileID)
	for _, part := range []schema.MessageInputPart{image, audio, video} {
		_, err := buildMessageFromUserInputMultiContent(&schema.Message{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{part}})
		require.ErrorContains(t, err, "FileID is not supported")
	}
}

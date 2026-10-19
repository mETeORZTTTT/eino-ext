# Uploaded file references (self-review draft)

This change consumes the `FileID` schema fields proposed in the companion Eino
change. It does not implement uploading, downloading, readiness polling,
renewal, or selection between provider accounts. The application must supply
an active file reference belonging to the service being called.

## Implemented request conversions

| Adapter | API | FileID input |
| --- | --- | --- |
| `components/model/ark` | Responses | Image, video, audio, file |
| `components/model/agenticark` | Responses | Image, video, audio, file |
| `components/model/agenticopenai` | Responses | Image and file in user inputs and function tool outputs |
| `libs/acl/openai` | Agentic-to-Message bridge | Preserve image/audio/video IDs; Chat encoding explicitly rejects unsupported FileID |

These are API encoding capabilities, not a guarantee that every hosted model
supports each modality. Use a model and account that support the selected input.
The existing URL and Base64 branches retain their behavior. A FileID input must
not also include URL or Base64Data. Whitespace-only IDs are rejected; otherwise
identifiers are forwarded unchanged. An omitted/empty Agentic FileID selects the
existing URL/Base64 path; a non-nil traditional FileID pointer must be nonempty.

File ID requests omit the alternative URL/data fields. Image detail and existing
video FPS metadata are preserved where available. Filename is omitted for an
already uploaded document, because its metadata belongs to the remote file.
Generate and Stream share the conversions. Provider errors, including expiry,
are returned through the existing error path.

## Deliberately not claimed by this draft

- Ark Chat: its current Go SDK request structures lack image/video FileID. The
  adapter returns an explicit error instead of treating a reference as a URL.
- Anthropic: the pinned SDK exposes file sources in Beta request types, while
  these adapters currently use ordinary Messages types. Supporting that route
  needs a separate API/SDK change and request-header review.
- Gemini: existing file URI inputs remain available through URL and MIMEType;
  bare uploaded file names are not converted into URIs here.
- DeepSeek, Kimi, GLM, Qwen and other gateways: no generic OpenAI-compatible
  FileID mapping is inferred from BaseURL. Their wire formats need separate
  adapter work. This is not full-provider FileID support.
- The deprecated Message.MultiContent representation is not extended.

## Testing

Run each changed module with the companion Eino schema change available. The
self-review branches use explicit dependency replacements for reproducibility;
replace them with released upstream module versions before upstream submission.

Tests cover the actual SDK JSON, source conflicts, old URL behavior, the
Agentic bridge, and real Generate/Stream HTTP requests to a local test server.
The OpenAI request test also executes an enhanced tool through the core
AgenticToolsNode and verifies its image/file references in the final function
call output. The local server returns a file-expiry error to check that it reaches callers.
No live-provider request or billable model invocation is made by these tests.

```sh
GOWORK=off go test -gcflags='all=-N -l' ./...
```

Run the command in `components/model/ark`, `components/model/agenticark`,
`components/model/agenticopenai`, and `libs/acl/openai`. The compiler flags are
required by existing mockey-based tests. The baseline agenticopenai tests use
`testing.Context` despite declaring Go 1.22; on Go 1.25, `go vet` reports that
pre-existing stdversion mismatch. Other vet checks can be run with
`go vet -stdversion=false ./...` while that baseline issue is handled separately.

## API references

- [Ark Responses](https://docs.volcengine.com/docs/ark/create-model-responses-api?lang=zh)
- [OpenAI image inputs](https://developers.openai.com/api/docs/guides/images-vision)
- [OpenAI file inputs](https://developers.openai.com/api/docs/guides/file-inputs)
- [Anthropic Files](https://platform.claude.com/docs/en/build-with-claude/files)
- [Gemini Files](https://ai.google.dev/gemini-api/docs/files)

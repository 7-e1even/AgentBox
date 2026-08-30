package httpapi

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strings"

	cliproxytranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/translator/builtin"
	"github.com/skys-mission/lmm-adapter-go/adapter"
	"github.com/skys-mission/lmm-adapter-go/stream"
)

type runtimeLLMProtocol string

const (
	runtimeLLMProtocolAnthropic runtimeLLMProtocol = "anthropic"
	runtimeLLMProtocolResponses runtimeLLMProtocol = "openai-responses"
	runtimeLLMProtocolChat      runtimeLLMProtocol = "openai-chat"
	runtimeLLMProtocolGemini    runtimeLLMProtocol = "gemini"
)

var runtimeLLMCLIProxyRegistry = builtin.Registry()

const runtimeLLMDocumentURLMediaType = "application/x-agentbox-document-url"

func runtimeLLMProtocolForCredential(protocol string) (runtimeLLMProtocol, error) {
	switch protocol {
	case string(runtimeLLMProtocolAnthropic):
		return runtimeLLMProtocolAnthropic, nil
	case string(runtimeLLMProtocolResponses):
		return runtimeLLMProtocolResponses, nil
	case string(runtimeLLMProtocolChat):
		return runtimeLLMProtocolChat, nil
	case string(runtimeLLMProtocolGemini):
		return runtimeLLMProtocolGemini, nil
	default:
		return "", fmt.Errorf("凭据协议 %q 暂不支持 LLM 格式转换", protocol)
	}
}

func (protocol runtimeLLMProtocol) cliProxyClientFormat() (cliproxytranslator.Format, error) {
	switch protocol {
	case runtimeLLMProtocolAnthropic:
		return cliproxytranslator.FormatClaude, nil
	case runtimeLLMProtocolResponses:
		return cliproxytranslator.FormatOpenAIResponse, nil
	case runtimeLLMProtocolChat:
		return cliproxytranslator.FormatOpenAI, nil
	case runtimeLLMProtocolGemini:
		return cliproxytranslator.FormatGemini, nil
	default:
		return "", fmt.Errorf("不支持的 LLM 协议 %q", protocol)
	}
}

func (protocol runtimeLLMProtocol) cliProxyUpstreamFormat() (cliproxytranslator.Format, error) {
	if protocol == runtimeLLMProtocolResponses {
		return cliproxytranslator.FormatCodex, nil
	}
	return protocol.cliProxyClientFormat()
}

func (protocol runtimeLLMProtocol) adapterProtocol() (adapter.Protocol, bool) {
	switch protocol {
	case runtimeLLMProtocolAnthropic:
		return adapter.ProtocolClaudeMessages, true
	case runtimeLLMProtocolResponses:
		return adapter.ProtocolOpenAIResponses, true
	case runtimeLLMProtocolChat:
		return adapter.ProtocolOpenAIChat, true
	default:
		return "", false
	}
}

func convertRuntimeLLMRequest(
	from, to runtimeLLMProtocol,
	model string,
	body []byte,
	streaming bool,
) ([]byte, error) {
	normalized, err := normalizeRuntimeLLMRequestForCLIProxy(from, to, body)
	if err != nil {
		return nil, err
	}
	fromFormat, err := from.cliProxyClientFormat()
	if err != nil {
		return nil, err
	}
	toFormat, err := to.cliProxyUpstreamFormat()
	if err != nil {
		return nil, err
	}
	if !runtimeLLMCLIProxyRegistry.HasResponseTransformer(fromFormat, toFormat) {
		return nil, fmt.Errorf("CLIProxyAPI 不支持 %s 到 %s 的请求转换", from, to)
	}
	converted := runtimeLLMCLIProxyRegistry.TranslateRequest(fromFormat, toFormat, model, normalized, streaming)
	if !json.Valid(converted) {
		return nil, errors.New("CLIProxyAPI 返回了无效的请求 JSON")
	}
	converted, err = restoreRuntimeLLMMultimodalRequest(from, to, body, converted)
	if err != nil {
		return nil, err
	}
	return converted, nil
}

func normalizeRuntimeLLMRequestForCLIProxy(from, to runtimeLLMProtocol, body []byte) ([]byte, error) {
	if from == to {
		return body, nil
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("请求 JSON 无效: %w", err)
	}

	var err error
	switch from {
	case runtimeLLMProtocolResponses:
		err = normalizeResponsesFiles(root)
	case runtimeLLMProtocolAnthropic:
		normalizeAnthropicDocuments(root)
	case runtimeLLMProtocolChat:
		if to != runtimeLLMProtocolGemini {
			err = normalizeChatFiles(root)
		}
	case runtimeLLMProtocolGemini:
		normalizeGeminiContent(root, to, body)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(root)
}

func normalizeGeminiContent(root map[string]any, to runtimeLLMProtocol, body []byte) {
	mediaIndex := 0
	for _, contentValue := range anySlice(root["contents"]) {
		content, _ := contentValue.(map[string]any)
		for _, partValue := range anySlice(content["parts"]) {
			part, _ := partValue.(map[string]any)
			if inlineData, ok := part["inlineData"].(map[string]any); ok {
				part["inline_data"] = map[string]any{
					"mime_type": firstNonEmptyRuntimeLLM(
						stringValue(inlineData["mimeType"]), stringValue(inlineData["mime_type"]),
					),
					"data": stringValue(inlineData["data"]),
				}
				delete(part, "inlineData")
			}
			if fileData, ok := part["fileData"].(map[string]any); ok {
				part["file_data"] = map[string]any{
					"mime_type": firstNonEmptyRuntimeLLM(
						stringValue(fileData["mimeType"]), stringValue(fileData["mime_type"]),
					),
					"file_uri": firstNonEmptyRuntimeLLM(
						stringValue(fileData["fileUri"]), stringValue(fileData["file_uri"]),
					),
				}
				delete(part, "fileData")
			}
			if to == runtimeLLMProtocolResponses || to == runtimeLLMProtocolChat {
				if _, ok := part["inline_data"].(map[string]any); ok {
					clear(part)
					part["text"] = runtimeLLMMediaMarker(body, mediaIndex)
					mediaIndex++
					continue
				}
				if _, ok := part["file_data"].(map[string]any); ok {
					clear(part)
					part["text"] = runtimeLLMMediaMarker(body, mediaIndex)
					mediaIndex++
				}
			}
		}
	}
}

func normalizeResponsesFiles(root map[string]any) error {
	for _, itemValue := range anySlice(root["input"]) {
		item, _ := itemValue.(map[string]any)
		for _, partValue := range anySlice(item["content"]) {
			part, _ := partValue.(map[string]any)
			if stringValue(part["type"]) != "input_file" {
				continue
			}
			dataURL, err := portableFileDataURL(part, "file_data", "file_url", "file_id")
			if err != nil {
				return err
			}
			part["type"] = "input_image"
			part["image_url"] = dataURL
			delete(part, "file_data")
			delete(part, "file_url")
			delete(part, "file_id")
		}
	}
	return nil
}

func normalizeAnthropicDocuments(root map[string]any) {
	for _, messageValue := range anySlice(root["messages"]) {
		message, _ := messageValue.(map[string]any)
		for _, partValue := range anySlice(message["content"]) {
			part, _ := partValue.(map[string]any)
			if stringValue(part["type"]) != "document" {
				continue
			}
			source, _ := part["source"].(map[string]any)
			switch stringValue(source["type"]) {
			case "url":
				url := stringValue(source["url"])
				part["source"] = map[string]any{
					"type":       "base64",
					"media_type": runtimeLLMDocumentURLMediaType,
					"data":       base64.StdEncoding.EncodeToString([]byte(url)),
				}
			case "text":
				part["source"] = map[string]any{
					"type":       "base64",
					"media_type": firstNonEmptyRuntimeLLM(stringValue(source["media_type"]), "text/plain"),
					"data":       base64.StdEncoding.EncodeToString([]byte(stringValue(source["data"]))),
				}
			}
			part["type"] = "image"
		}
	}
}

func normalizeChatFiles(root map[string]any) error {
	for _, messageValue := range anySlice(root["messages"]) {
		message, _ := messageValue.(map[string]any)
		for _, partValue := range anySlice(message["content"]) {
			part, _ := partValue.(map[string]any)
			if stringValue(part["type"]) != "file" {
				continue
			}
			file, _ := part["file"].(map[string]any)
			dataURL, err := portableFileDataURL(file, "file_data", "file_url", "file_id")
			if err != nil {
				return err
			}
			part["type"] = "image_url"
			part["image_url"] = map[string]any{"url": dataURL}
			delete(part, "file")
		}
	}
	return nil
}

func portableFileDataURL(part map[string]any, dataKey, urlKey, idKey string) (string, error) {
	if data := stringValue(part[dataKey]); data != "" {
		if strings.HasPrefix(data, "data:") {
			return data, nil
		}
		mediaType := mediaTypeForFilename(stringValue(part["filename"]))
		return "data:" + mediaType + ";base64," + data, nil
	}
	if url := stringValue(part[urlKey]); url != "" {
		return "data:" + runtimeLLMDocumentURLMediaType + ";base64," +
			base64.StdEncoding.EncodeToString([]byte(url)), nil
	}
	if id := stringValue(part[idKey]); id != "" {
		return "", fmt.Errorf("文件 %q 只有供应商专用 file_id，跨协议转换需要 file_data 或 file_url", id)
	}
	return "", errors.New("input_file 缺少 file_data 或 file_url")
}

func restoreRuntimeLLMMultimodalRequest(
	from, to runtimeLLMProtocol,
	original, converted []byte,
) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(converted, &root); err != nil {
		return nil, fmt.Errorf("解析 CLIProxyAPI 请求失败: %w", err)
	}

	restorePortableDocuments(to, root)
	switch {
	case from == runtimeLLMProtocolResponses && to == runtimeLLMProtocolAnthropic:
		restoreResponsesToolResultsInAnthropic(original, root)
	case from == runtimeLLMProtocolAnthropic && to == runtimeLLMProtocolResponses:
		restoreAnthropicToolResultsInResponses(original, root)
	case from == runtimeLLMProtocolChat && to == runtimeLLMProtocolAnthropic:
		restoreChatToolResultsInAnthropic(original, root)
	case from == runtimeLLMProtocolGemini &&
		(to == runtimeLLMProtocolResponses || to == runtimeLLMProtocolChat):
		restoreGeminiMedia(original, to, root)
	}
	if to == runtimeLLMProtocolAnthropic {
		sanitizeAnthropicToolNames(root)
	}
	return json.Marshal(root)
}

func sanitizeAnthropicToolNames(root map[string]any) {
	tools := anySlice(root["tools"])
	if len(tools) == 0 {
		return
	}
	replacements := make(map[string]string)
	used := make(map[string]bool)
	validTools := make([]any, 0, len(tools))
	for _, toolValue := range tools {
		tool, _ := toolValue.(map[string]any)
		original := stringValue(tool["name"])
		if strings.TrimSpace(original) == "" {
			continue
		}
		normalized := uniqueAnthropicToolName(original, used)
		tool["name"] = normalized
		replacements[original] = normalized
		validTools = append(validTools, tool)
	}
	if len(validTools) == 0 {
		delete(root, "tools")
		delete(root, "tool_choice")
		return
	}
	root["tools"] = validTools
	walkAnthropicContent(root, func(part map[string]any) {
		if stringValue(part["type"]) != "tool_use" {
			return
		}
		if normalized := replacements[stringValue(part["name"])]; normalized != "" {
			part["name"] = normalized
		}
	})
	if toolChoice, ok := root["tool_choice"].(map[string]any); ok {
		if normalized := replacements[stringValue(toolChoice["name"])]; normalized != "" {
			toolChoice["name"] = normalized
		}
	}
}

func uniqueAnthropicToolName(original string, used map[string]bool) string {
	var builder strings.Builder
	for _, character := range original {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' {
			builder.WriteRune(character)
		} else {
			builder.WriteByte('_')
		}
	}
	name := strings.Trim(builder.String(), "_-")
	if name == "" || !isASCIILetter(name[0]) {
		name = "tool_" + name
	}
	name = strings.TrimRight(name, "_-")
	if len(name) > 64 {
		name = strings.TrimRight(name[:64], "_-")
	}
	if !used[name] {
		used[name] = true
		return name
	}
	hash := sha256.Sum256([]byte(original))
	suffix := fmt.Sprintf("_%x", hash[:4])
	if len(name)+len(suffix) > 64 {
		name = strings.TrimRight(name[:64-len(suffix)], "_-")
	}
	name += suffix
	for index := 2; used[name]; index++ {
		counter := fmt.Sprintf("_%d", index)
		base := strings.TrimSuffix(name, suffix)
		if len(base)+len(suffix)+len(counter) > 64 {
			base = strings.TrimRight(base[:64-len(suffix)-len(counter)], "_-")
		}
		name = base + suffix + counter
	}
	used[name] = true
	return name
}

func isASCIILetter(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z')
}

func runtimeLLMResponseToolNameMap(
	client runtimeLLMProtocol, originalRequest, translatedRequest []byte,
) map[string]string {
	originalNames := runtimeLLMRequestToolNames(client, originalRequest)
	var translated map[string]any
	if len(originalNames) == 0 || json.Unmarshal(translatedRequest, &translated) != nil {
		return nil
	}
	translatedTools := anySlice(translated["tools"])
	result := make(map[string]string)
	for index, toolValue := range translatedTools {
		if index >= len(originalNames) {
			break
		}
		tool, _ := toolValue.(map[string]any)
		translatedName := stringValue(tool["name"])
		if translatedName != "" && originalNames[index] != "" && translatedName != originalNames[index] {
			result[translatedName] = originalNames[index]
		}
	}
	return result
}

func runtimeLLMRequestToolNames(protocol runtimeLLMProtocol, body []byte) []string {
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return nil
	}
	var names []string
	switch protocol {
	case runtimeLLMProtocolAnthropic:
		for _, toolValue := range anySlice(root["tools"]) {
			tool, _ := toolValue.(map[string]any)
			if name := stringValue(tool["name"]); name != "" {
				names = append(names, name)
			}
		}
	case runtimeLLMProtocolResponses:
		for _, toolValue := range anySlice(root["tools"]) {
			tool, _ := toolValue.(map[string]any)
			name := firstNonEmptyRuntimeLLM(stringValue(tool["name"]), nestedString(tool, "function", "name"))
			if name != "" {
				names = append(names, name)
			}
		}
	case runtimeLLMProtocolChat:
		for _, toolValue := range anySlice(root["tools"]) {
			tool, _ := toolValue.(map[string]any)
			if name := nestedString(tool, "function", "name"); name != "" {
				names = append(names, name)
			}
		}
	case runtimeLLMProtocolGemini:
		for _, groupValue := range anySlice(root["tools"]) {
			group, _ := groupValue.(map[string]any)
			declarations := firstNonNilSlice(group["functionDeclarations"], group["function_declarations"])
			for _, declarationValue := range declarations {
				declaration, _ := declarationValue.(map[string]any)
				if name := stringValue(declaration["name"]); name != "" {
					names = append(names, name)
				}
			}
		}
	}
	return names
}

func restoreRuntimeLLMResponseToolNames(
	client runtimeLLMProtocol, originalRequest, translatedRequest, response []byte,
) []byte {
	names := runtimeLLMResponseToolNameMap(client, originalRequest, translatedRequest)
	if len(names) == 0 {
		return response
	}
	var root map[string]any
	if json.Unmarshal(response, &root) != nil {
		return response
	}
	restoreRuntimeLLMToolNamesInJSON(client, root, names)
	converted, err := json.Marshal(root)
	if err != nil {
		return response
	}
	return converted
}

func restoreRuntimeLLMToolNamesInJSON(
	protocol runtimeLLMProtocol, root map[string]any, names map[string]string,
) {
	restore := func(object map[string]any, key string) {
		if original := names[stringValue(object[key])]; original != "" {
			object[key] = original
		}
	}
	switch protocol {
	case runtimeLLMProtocolResponses:
		for _, itemValue := range anySlice(root["output"]) {
			item, _ := itemValue.(map[string]any)
			restore(item, "name")
		}
		if item, ok := root["item"].(map[string]any); ok {
			restore(item, "name")
		}
		restore(root, "name")
	case runtimeLLMProtocolChat:
		for _, choiceValue := range anySlice(root["choices"]) {
			choice, _ := choiceValue.(map[string]any)
			for _, containerKey := range []string{"message", "delta"} {
				container, _ := choice[containerKey].(map[string]any)
				for _, callValue := range anySlice(container["tool_calls"]) {
					call, _ := callValue.(map[string]any)
					function, _ := call["function"].(map[string]any)
					restore(function, "name")
				}
			}
		}
	case runtimeLLMProtocolGemini:
		for _, candidateValue := range anySlice(root["candidates"]) {
			candidate, _ := candidateValue.(map[string]any)
			content, _ := candidate["content"].(map[string]any)
			for _, partValue := range anySlice(content["parts"]) {
				part, _ := partValue.(map[string]any)
				function, _ := firstMap(part["functionCall"], part["function_call"])
				restore(function, "name")
			}
		}
	}
}

func nestedString(root map[string]any, keys ...string) string {
	current := root
	for index, key := range keys {
		if index == len(keys)-1 {
			return stringValue(current[key])
		}
		next, _ := current[key].(map[string]any)
		current = next
	}
	return ""
}

func firstNonNilSlice(values ...any) []any {
	for _, value := range values {
		if items := anySlice(value); items != nil {
			return items
		}
	}
	return nil
}

func restoreGeminiMedia(original []byte, to runtimeLLMProtocol, converted map[string]any) {
	media := geminiMediaParts(original, to)
	if len(media) == 0 {
		return
	}
	replace := func(part map[string]any) bool {
		index, ok := runtimeLLMMediaMarkerIndex(stringValue(part["text"]), original, len(media))
		if !ok {
			return false
		}
		clear(part)
		for key, value := range media[index] {
			part[key] = value
		}
		return true
	}

	switch to {
	case runtimeLLMProtocolResponses:
		for _, itemValue := range anySlice(converted["input"]) {
			item, _ := itemValue.(map[string]any)
			for _, partValue := range anySlice(item["content"]) {
				part, _ := partValue.(map[string]any)
				replace(part)
			}
		}
	case runtimeLLMProtocolChat:
		for _, messageValue := range anySlice(converted["messages"]) {
			message, _ := messageValue.(map[string]any)
			if text, ok := message["content"].(string); ok {
				index, found := runtimeLLMMediaMarkerIndex(text, original, len(media))
				if found {
					message["content"] = []any{media[index]}
				}
				continue
			}
			for _, partValue := range anySlice(message["content"]) {
				part, _ := partValue.(map[string]any)
				replace(part)
			}
		}
	}
}

func geminiMediaParts(body []byte, to runtimeLLMProtocol) []map[string]any {
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return nil
	}
	var result []map[string]any
	for _, contentValue := range anySlice(root["contents"]) {
		content, _ := contentValue.(map[string]any)
		for _, partValue := range anySlice(content["parts"]) {
			part, _ := partValue.(map[string]any)
			inlineData, _ := firstMap(part["inlineData"], part["inline_data"])
			if inlineData != nil {
				mediaType := firstNonEmptyRuntimeLLM(
					stringValue(inlineData["mimeType"]), stringValue(inlineData["mime_type"]),
				)
				dataURL := "data:" + firstNonEmptyRuntimeLLM(mediaType, "application/octet-stream") +
					";base64," + stringValue(inlineData["data"])
				result = append(result, runtimeLLMTargetMediaPart(to, mediaType, dataURL, ""))
				continue
			}
			fileData, _ := firstMap(part["fileData"], part["file_data"])
			if fileData != nil {
				mediaType := firstNonEmptyRuntimeLLM(
					stringValue(fileData["mimeType"]), stringValue(fileData["mime_type"]),
				)
				uri := firstNonEmptyRuntimeLLM(
					stringValue(fileData["fileUri"]), stringValue(fileData["file_uri"]),
				)
				result = append(result, runtimeLLMTargetMediaPart(to, mediaType, "", uri))
			}
		}
	}
	return result
}

func runtimeLLMTargetMediaPart(
	to runtimeLLMProtocol, mediaType, dataURL, fileURL string,
) map[string]any {
	isImage := strings.HasPrefix(mediaType, "image/")
	switch to {
	case runtimeLLMProtocolResponses:
		if isImage && dataURL != "" {
			return map[string]any{"type": "input_image", "image_url": dataURL}
		}
		result := map[string]any{"type": "input_file"}
		if dataURL != "" {
			result["file_data"] = dataURL
			result["filename"] = "document" + extensionForMediaType(mediaType)
		} else {
			result["file_url"] = fileURL
		}
		return result
	case runtimeLLMProtocolChat:
		if isImage && dataURL != "" {
			return map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}}
		}
		file := map[string]any{"filename": "document" + extensionForMediaType(mediaType)}
		if dataURL != "" {
			file["file_data"] = dataURL
		} else {
			file["file_url"] = fileURL
		}
		return map[string]any{"type": "file", "file": file}
	default:
		return nil
	}
}

func runtimeLLMMediaMarker(body []byte, index int) string {
	hash := sha256.Sum256(body)
	return fmt.Sprintf("[[agentbox-media:%x:%d]]", hash[:8], index)
}

func runtimeLLMMediaMarkerIndex(value string, body []byte, count int) (int, bool) {
	for index := range count {
		if value == runtimeLLMMediaMarker(body, index) {
			return index, true
		}
	}
	return 0, false
}

func firstMap(values ...any) (map[string]any, bool) {
	for _, value := range values {
		if object, ok := value.(map[string]any); ok {
			return object, true
		}
	}
	return nil, false
}

func restorePortableDocuments(to runtimeLLMProtocol, root map[string]any) {
	switch to {
	case runtimeLLMProtocolAnthropic:
		walkAnthropicContent(root, func(part map[string]any) {
			if stringValue(part["type"]) != "image" {
				return
			}
			source, _ := part["source"].(map[string]any)
			mediaType := stringValue(source["media_type"])
			if strings.HasPrefix(mediaType, "image/") || mediaType == "" {
				return
			}
			part["type"] = "document"
			if mediaType == runtimeLLMDocumentURLMediaType {
				if decoded, err := base64.StdEncoding.DecodeString(stringValue(source["data"])); err == nil {
					part["source"] = map[string]any{"type": "url", "url": string(decoded)}
				}
			}
		})
	case runtimeLLMProtocolResponses:
		for _, itemValue := range anySlice(root["input"]) {
			item, _ := itemValue.(map[string]any)
			restoreResponsesDocumentParts(anySlice(item["content"]))
			if stringValue(item["type"]) == "function_call_output" {
				restoreResponsesDocumentParts(anySlice(item["output"]))
			}
		}
	case runtimeLLMProtocolChat:
		for _, messageValue := range anySlice(root["messages"]) {
			message, _ := messageValue.(map[string]any)
			for _, partValue := range anySlice(message["content"]) {
				part, _ := partValue.(map[string]any)
				if stringValue(part["type"]) != "image_url" {
					continue
				}
				imageURL, _ := part["image_url"].(map[string]any)
				file := responseFilePartFromURL(stringValue(imageURL["url"]))
				if file == nil {
					continue
				}
				part["type"] = "file"
				part["file"] = file
				delete(part, "image_url")
			}
		}
	}
}

func walkAnthropicContent(root map[string]any, visit func(map[string]any)) {
	for _, messageValue := range anySlice(root["messages"]) {
		message, _ := messageValue.(map[string]any)
		for _, partValue := range anySlice(message["content"]) {
			part, _ := partValue.(map[string]any)
			visit(part)
			for _, nestedValue := range anySlice(part["content"]) {
				nested, _ := nestedValue.(map[string]any)
				visit(nested)
			}
		}
	}
}

func restoreResponsesDocumentParts(parts []any) {
	for _, partValue := range parts {
		part, _ := partValue.(map[string]any)
		if stringValue(part["type"]) != "input_image" {
			continue
		}
		file := responseFilePartFromURL(stringValue(part["image_url"]))
		if file == nil {
			continue
		}
		part["type"] = "input_file"
		for key, value := range file {
			part[key] = value
		}
		delete(part, "image_url")
	}
}

func responseFilePartFromURL(value string) map[string]any {
	mediaType, data, ok := parseDataURL(value)
	if !ok || strings.HasPrefix(mediaType, "image/") {
		return nil
	}
	if mediaType == runtimeLLMDocumentURLMediaType {
		decoded, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil
		}
		return map[string]any{"file_url": string(decoded)}
	}
	return map[string]any{
		"file_data": value,
		"filename":  "document" + extensionForMediaType(mediaType),
	}
}

func restoreResponsesToolResultsInAnthropic(original []byte, converted map[string]any) {
	outputs := responsesToolOutputs(original)
	walkAnthropicContent(converted, func(part map[string]any) {
		if stringValue(part["type"]) != "tool_result" {
			return
		}
		if output, ok := outputs[stringValue(part["tool_use_id"])]; ok {
			part["content"] = responsesContentToAnthropic(output)
		}
	})
}

func restoreAnthropicToolResultsInResponses(original []byte, converted map[string]any) {
	results := anthropicToolResults(original)
	for _, itemValue := range anySlice(converted["input"]) {
		item, _ := itemValue.(map[string]any)
		if stringValue(item["type"]) != "function_call_output" {
			continue
		}
		if content, ok := results[stringValue(item["call_id"])]; ok {
			item["output"] = anthropicContentToResponses(content)
		}
	}
}

func restoreChatToolResultsInAnthropic(original []byte, converted map[string]any) {
	results := chatToolResults(original)
	walkAnthropicContent(converted, func(part map[string]any) {
		if stringValue(part["type"]) != "tool_result" {
			return
		}
		if content, ok := results[stringValue(part["tool_use_id"])]; ok {
			part["content"] = chatContentToAnthropic(content)
		}
	})
}

func responsesToolOutputs(body []byte) map[string]any {
	result := make(map[string]any)
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return result
	}
	for _, itemValue := range anySlice(root["input"]) {
		item, _ := itemValue.(map[string]any)
		if stringValue(item["type"]) == "function_call_output" {
			result[stringValue(item["call_id"])] = item["output"]
		}
	}
	return result
}

func anthropicToolResults(body []byte) map[string]any {
	result := make(map[string]any)
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return result
	}
	for _, messageValue := range anySlice(root["messages"]) {
		message, _ := messageValue.(map[string]any)
		for _, partValue := range anySlice(message["content"]) {
			part, _ := partValue.(map[string]any)
			if stringValue(part["type"]) == "tool_result" {
				result[stringValue(part["tool_use_id"])] = part["content"]
			}
		}
	}
	return result
}

func chatToolResults(body []byte) map[string]any {
	result := make(map[string]any)
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return result
	}
	for _, messageValue := range anySlice(root["messages"]) {
		message, _ := messageValue.(map[string]any)
		if stringValue(message["role"]) == "tool" {
			result[stringValue(message["tool_call_id"])] = message["content"]
		}
	}
	return result
}

func responsesContentToAnthropic(content any) any {
	if _, ok := content.(string); ok {
		return content
	}
	result := make([]any, 0)
	for _, partValue := range anySlice(content) {
		part, _ := partValue.(map[string]any)
		switch stringValue(part["type"]) {
		case "input_text", "output_text", "text":
			result = append(result, map[string]any{"type": "text", "text": stringValue(part["text"])})
		case "input_image":
			if block := anthropicImageBlock(stringValue(part["image_url"])); block != nil {
				result = append(result, block)
			}
		case "input_file":
			if block := anthropicDocumentBlock(part); block != nil {
				result = append(result, block)
			}
		default:
			result = append(result, map[string]any{"type": "text", "text": compactJSON(partValue)})
		}
	}
	return result
}

func anthropicContentToResponses(content any) any {
	if _, ok := content.(string); ok {
		return content
	}
	result := make([]any, 0)
	for _, partValue := range anySlice(content) {
		part, _ := partValue.(map[string]any)
		switch stringValue(part["type"]) {
		case "text":
			result = append(result, map[string]any{"type": "input_text", "text": stringValue(part["text"])})
		case "image":
			if url := anthropicSourceURL(part); url != "" {
				result = append(result, map[string]any{"type": "input_image", "image_url": url})
			}
		case "document":
			if block := responsesFileBlockFromAnthropic(part); block != nil {
				result = append(result, block)
			}
		default:
			result = append(result, map[string]any{"type": "input_text", "text": compactJSON(partValue)})
		}
	}
	return result
}

func chatContentToAnthropic(content any) any {
	if _, ok := content.(string); ok {
		return content
	}
	result := make([]any, 0)
	for _, partValue := range anySlice(content) {
		part, _ := partValue.(map[string]any)
		switch stringValue(part["type"]) {
		case "text":
			result = append(result, map[string]any{"type": "text", "text": stringValue(part["text"])})
		case "image_url":
			imageURL, _ := part["image_url"].(map[string]any)
			if block := anthropicImageBlock(stringValue(imageURL["url"])); block != nil {
				result = append(result, block)
			}
		case "file":
			file, _ := part["file"].(map[string]any)
			if block := anthropicDocumentBlock(file); block != nil {
				result = append(result, block)
			}
		default:
			result = append(result, map[string]any{"type": "text", "text": compactJSON(partValue)})
		}
	}
	return result
}

func anthropicImageBlock(url string) map[string]any {
	mediaType, data, ok := parseDataURL(url)
	if ok {
		if !strings.HasPrefix(mediaType, "image/") {
			return map[string]any{
				"type":   "document",
				"source": map[string]any{"type": "base64", "media_type": mediaType, "data": data},
			}
		}
		return map[string]any{
			"type":   "image",
			"source": map[string]any{"type": "base64", "media_type": mediaType, "data": data},
		}
	}
	if url != "" {
		return map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": url}}
	}
	return nil
}

func anthropicDocumentBlock(file map[string]any) map[string]any {
	if data := firstNonEmptyRuntimeLLM(stringValue(file["file_data"]), stringValue(file["data"])); data != "" {
		mediaType, encoded, ok := parseDataURL(data)
		if !ok {
			mediaType, encoded = mediaTypeForFilename(stringValue(file["filename"])), data
		}
		return map[string]any{
			"type":   "document",
			"source": map[string]any{"type": "base64", "media_type": mediaType, "data": encoded},
		}
	}
	if url := firstNonEmptyRuntimeLLM(stringValue(file["file_url"]), stringValue(file["url"])); url != "" {
		return map[string]any{"type": "document", "source": map[string]any{"type": "url", "url": url}}
	}
	return nil
}

func responsesFileBlockFromAnthropic(part map[string]any) map[string]any {
	source, _ := part["source"].(map[string]any)
	switch stringValue(source["type"]) {
	case "base64":
		mediaType := firstNonEmptyRuntimeLLM(stringValue(source["media_type"]), "application/octet-stream")
		return map[string]any{
			"type":      "input_file",
			"file_data": "data:" + mediaType + ";base64," + stringValue(source["data"]),
			"filename":  "document" + extensionForMediaType(mediaType),
		}
	case "url":
		return map[string]any{"type": "input_file", "file_url": stringValue(source["url"])}
	case "text":
		mediaType := firstNonEmptyRuntimeLLM(stringValue(source["media_type"]), "text/plain")
		return map[string]any{
			"type":      "input_file",
			"file_data": "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString([]byte(stringValue(source["data"]))),
			"filename":  "document" + extensionForMediaType(mediaType),
		}
	}
	return nil
}

func anthropicSourceURL(part map[string]any) string {
	source, _ := part["source"].(map[string]any)
	if stringValue(source["type"]) == "url" {
		return stringValue(source["url"])
	}
	if data := stringValue(source["data"]); data != "" {
		mediaType := firstNonEmptyRuntimeLLM(stringValue(source["media_type"]), "application/octet-stream")
		return "data:" + mediaType + ";base64," + data
	}
	return ""
}

func parseDataURL(value string) (mediaType, data string, ok bool) {
	value, ok = strings.CutPrefix(value, "data:")
	if !ok {
		return "", "", false
	}
	metadata, data, found := strings.Cut(value, ",")
	if !found || !strings.HasSuffix(strings.ToLower(metadata), ";base64") {
		return "", "", false
	}
	mediaType = strings.TrimSuffix(metadata, ";base64")
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return mediaType, data, true
}

func mediaTypeForFilename(filename string) string {
	if mediaType := mime.TypeByExtension(filepath.Ext(filename)); mediaType != "" {
		return strings.TrimSpace(strings.Split(mediaType, ";")[0])
	}
	return "application/octet-stream"
}

func extensionForMediaType(mediaType string) string {
	if extensions, _ := mime.ExtensionsByType(mediaType); len(extensions) > 0 {
		return extensions[0]
	}
	return ".bin"
}

func compactJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func anySlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func firstNonEmptyRuntimeLLM(values ...string) string {
	return cmp.Or(values...)
}

func convertRuntimeLLMResponse(
	ctx context.Context,
	from, to runtimeLLMProtocol,
	model string,
	originalRequest, translatedRequest, body []byte,
) ([]byte, error) {
	if to == runtimeLLMProtocolGemini && from != runtimeLLMProtocolGemini {
		converted, err := convertRuntimeLLMResponseToGemini(ctx, from, model, originalRequest, body)
		if err == nil && from == runtimeLLMProtocolAnthropic {
			converted = restoreRuntimeLLMResponseToolNames(to, originalRequest, translatedRequest, converted)
		}
		return converted, err
	}
	fromAdapter, fromSupported := from.adapterProtocol()
	toAdapter, toSupported := to.adapterProtocol()
	if fromSupported && toSupported {
		converted, _, err := runtimeLLMConverter.ConvertResponse(fromAdapter, toAdapter, body)
		if err == nil && from == runtimeLLMProtocolAnthropic {
			converted = restoreRuntimeLLMResponseToolNames(to, originalRequest, translatedRequest, converted)
		}
		return converted, err
	}
	fromFormat, err := from.cliProxyUpstreamFormat()
	if err != nil {
		return nil, err
	}
	toFormat, err := to.cliProxyClientFormat()
	if err != nil {
		return nil, err
	}
	if !runtimeLLMCLIProxyRegistry.HasResponseTransformer(toFormat, fromFormat) {
		return nil, fmt.Errorf("CLIProxyAPI 不支持 %s 到 %s 的响应转换", from, to)
	}
	var state any
	converted := []byte(runtimeLLMCLIProxyRegistry.TranslateNonStream(
		ctx, fromFormat, toFormat, model, originalRequest, translatedRequest, body, &state,
	))
	if !json.Valid(converted) {
		return nil, errors.New("CLIProxyAPI 返回了无效的响应 JSON")
	}
	if from == runtimeLLMProtocolAnthropic {
		converted = restoreRuntimeLLMResponseToolNames(to, originalRequest, translatedRequest, converted)
	}
	return converted, nil
}

func convertRuntimeLLMResponseToGemini(
	ctx context.Context,
	from runtimeLLMProtocol,
	model string,
	originalRequest, body []byte,
) ([]byte, error) {
	chatResponse := body
	if from != runtimeLLMProtocolChat {
		fromAdapter, ok := from.adapterProtocol()
		if !ok {
			return nil, fmt.Errorf("不支持将 %s 响应转换为 Gemini", from)
		}
		converted, _, err := runtimeLLMConverter.ConvertResponse(
			fromAdapter, adapter.ProtocolOpenAIChat, body,
		)
		if err != nil {
			return nil, err
		}
		chatResponse = converted
	}
	chatRequest, err := convertRuntimeLLMRequest(
		runtimeLLMProtocolGemini, runtimeLLMProtocolChat, model, originalRequest, false,
	)
	if err != nil {
		return nil, err
	}
	var state any
	converted := []byte(runtimeLLMCLIProxyRegistry.TranslateNonStream(
		ctx,
		cliproxytranslator.FormatOpenAI,
		cliproxytranslator.FormatGemini,
		model,
		originalRequest,
		chatRequest,
		chatResponse,
		&state,
	))
	if !json.Valid(converted) {
		return nil, errors.New("CLIProxyAPI 返回了无效的 Gemini 响应 JSON")
	}
	return converted, nil
}

func (s *Server) convertRuntimeLLMCLIProxyStream(
	w io.Writer,
	ctx context.Context,
	reader io.Reader,
	model string,
	from, to runtimeLLMProtocol,
	originalRequest, translatedRequest []byte,
) error {
	fromFormat, err := from.cliProxyUpstreamFormat()
	if err != nil {
		return err
	}
	toFormat, err := to.cliProxyClientFormat()
	if err != nil {
		return err
	}
	if !runtimeLLMCLIProxyRegistry.HasResponseTransformer(toFormat, fromFormat) {
		return fmt.Errorf("CLIProxyAPI 不支持 %s 到 %s 的流式响应转换", from, to)
	}
	sseReader := stream.NewSSEReader(reader)
	var state any
	toolNames := map[string]string(nil)
	if from == runtimeLLMProtocolAnthropic {
		toolNames = runtimeLLMResponseToolNameMap(to, originalRequest, translatedRequest)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		event, readErr := sseReader.Read()
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("读取模型流失败: %w", readErr)
		}
		if event.Data == "" {
			continue
		}
		raw := []byte("data: " + event.Data)
		if event.Data == "[DONE]" {
			raw = []byte("[DONE]")
		}
		chunks := runtimeLLMCLIProxyRegistry.TranslateStream(
			ctx, fromFormat, toFormat, model, originalRequest, translatedRequest, raw, &state,
		)
		for _, chunk := range chunks {
			chunk = restoreRuntimeLLMStreamToolNames(to, chunk, toolNames)
			if err := writeRuntimeLLMCLIProxyChunk(w, to, chunk); err != nil {
				return err
			}
		}
	}
}

func restoreRuntimeLLMStreamToolNames(
	protocol runtimeLLMProtocol, chunk string, names map[string]string,
) string {
	if len(names) == 0 {
		return chunk
	}
	restoreJSON := func(value string) string {
		var root map[string]any
		if json.Unmarshal([]byte(value), &root) != nil {
			return value
		}
		restoreRuntimeLLMToolNamesInJSON(protocol, root, names)
		converted, err := json.Marshal(root)
		if err != nil {
			return value
		}
		return string(converted)
	}
	trimmed := strings.TrimSpace(chunk)
	if strings.HasPrefix(trimmed, "data:") || strings.HasPrefix(trimmed, "event:") {
		lines := strings.Split(trimmed, "\n")
		for index, line := range lines {
			if data, ok := strings.CutPrefix(line, "data:"); ok {
				payload := strings.TrimSpace(data)
				if payload != "" && payload != "[DONE]" {
					lines[index] = "data: " + restoreJSON(payload)
				}
			}
		}
		return strings.Join(lines, "\n")
	}
	if trimmed == "[DONE]" {
		return chunk
	}
	return restoreJSON(trimmed)
}

func writeRuntimeLLMCLIProxyChunk(w io.Writer, protocol runtimeLLMProtocol, chunk string) error {
	chunk = strings.TrimSpace(chunk)
	if chunk == "" {
		return nil
	}
	if strings.HasPrefix(chunk, "data:") || strings.HasPrefix(chunk, "event:") {
		_, err := io.WriteString(w, chunk+"\n\n")
		return err
	}
	if !json.Valid([]byte(chunk)) && chunk != "[DONE]" {
		return errors.New("CLIProxyAPI 返回了无效的流式事件")
	}
	if protocol == runtimeLLMProtocolAnthropic && chunk != "[DONE]" {
		var envelope struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(chunk), &envelope)
		if envelope.Type != "" {
			_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", envelope.Type, chunk)
			return err
		}
	}
	_, err := fmt.Fprintf(w, "data: %s\n\n", chunk)
	return err
}

func runtimeLLMErrorBody(protocol runtimeLLMProtocol, status int, message string) []byte {
	var payload any
	switch protocol {
	case runtimeLLMProtocolAnthropic:
		payload = map[string]any{
			"type":  "error",
			"error": map[string]string{"type": "api_error", "message": message},
		}
	case runtimeLLMProtocolGemini:
		payload = map[string]any{
			"error": map[string]any{"code": status, "message": message, "status": "UNKNOWN"},
		}
	default:
		payload = map[string]any{
			"error": map[string]string{"type": "api_error", "message": message},
		}
	}
	body, _ := json.Marshal(payload)
	return append(body, '\n')
}

func runtimeLLMErrorMessage(body []byte) string {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return "模型服务请求失败"
	}
	if message, ok := payload["message"].(string); ok && strings.TrimSpace(message) != "" {
		return message
	}
	switch value := payload["error"].(type) {
	case string:
		if strings.TrimSpace(value) != "" {
			return value
		}
	case map[string]any:
		if message, ok := value["message"].(string); ok && strings.TrimSpace(message) != "" {
			return message
		}
	}
	return "模型服务请求失败"
}

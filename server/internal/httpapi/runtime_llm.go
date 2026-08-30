package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"agentbox/internal/platform"
	"agentbox/internal/store"
	"github.com/skys-mission/lmm-adapter-go/adapter"
	"github.com/skys-mission/lmm-adapter-go/adapter/claude"
	"github.com/skys-mission/lmm-adapter-go/adapter/openaichat"
	"github.com/skys-mission/lmm-adapter-go/adapter/openairesp"
	"github.com/skys-mission/lmm-adapter-go/stream"
	"github.com/skys-mission/lmm-adapter-go/token"
)

const (
	runtimeLLMMaxBodyBytes = 32 << 20
	// runtimeLLMNonStreamingTimeout 限制非流式请求的整体耗时（连接+读完整响应体），
	// 流式请求不受此限制，由连接保活与客户端断开控制。
	runtimeLLMNonStreamingTimeout = 2 * time.Minute
)

var runtimeLLMConverter = adapter.NewConverter(
	adapter.WithAdapter(claude.New()),
	adapter.WithAdapter(openaichat.New()),
	adapter.WithAdapter(openairesp.New()),
)

func (s *Server) runtimeLLMAnthropic(w http.ResponseWriter, request *http.Request) {
	s.runtimeLLM(w, request, runtimeLLMProtocolAnthropic)
}

func (s *Server) runtimeLLMResponses(w http.ResponseWriter, request *http.Request) {
	s.runtimeLLM(w, request, runtimeLLMProtocolResponses)
}

func (s *Server) runtimeLLMChat(w http.ResponseWriter, request *http.Request) {
	s.runtimeLLM(w, request, runtimeLLMProtocolChat)
}

func (s *Server) runtimeLLMGemini(w http.ResponseWriter, request *http.Request) {
	target, ok := s.resolveRuntimeLLMTarget(w, request, runtimeLLMProtocolGemini)
	if !ok {
		return
	}
	upstreamProtocol, err := runtimeLLMProtocolForCredential(target.Protocol)
	if err != nil {
		s.writeRuntimeLLMError(w, runtimeLLMProtocolGemini, http.StatusBadRequest, err.Error())
		return
	}
	if upstreamProtocol == runtimeLLMProtocolGemini {
		s.forwardNativeRuntimeLLMGemini(w, request, target)
		return
	}
	body, ok := readRuntimeLLMBody(w, request)
	if !ok {
		return
	}
	streaming := runtimeLLMGeminiStreaming(request.PathValue("path"), request.URL.Query())
	converted, err := convertRuntimeLLMRequest(
		runtimeLLMProtocolGemini, upstreamProtocol, target.ModelID, body, streaming,
	)
	if err != nil {
		s.writeRuntimeLLMError(w, runtimeLLMProtocolGemini, http.StatusUnprocessableEntity, "Gemini 请求无法转换为凭据所需的协议")
		return
	}
	upstreamURL, err := runtimeLLMUpstreamURL(target, false)
	if err != nil {
		s.writeRuntimeLLMError(w, runtimeLLMProtocolGemini, http.StatusBadGateway, err.Error())
		return
	}
	s.setRuntimeLLMConversionHeaders(w.Header(), runtimeLLMProtocolGemini, upstreamProtocol)
	s.forwardRuntimeLLM(
		w, request, target, runtimeLLMProtocolGemini, upstreamProtocol,
		upstreamURL, body, converted, streaming,
	)
}

func (s *Server) forwardNativeRuntimeLLMGemini(
	w http.ResponseWriter, request *http.Request, target platform.RuntimeLLMTarget,
) {
	started := time.Now()
	var upstreamStatus int
	failure := ""
	defer func() {
		entry := platform.LogEntry{
			Category: platform.LogCategoryLLM, Action: "proxy",
			Message:      "LLM 代理请求 " + target.ModelID,
			ResourceKind: "credential", ResourceID: target.CredentialID,
			DurationMS: time.Since(started).Milliseconds(),
			Detail: map[string]any{
				"sandboxId": target.SandboxID, "provider": target.ProviderID,
				"protocol": "gemini", "model": target.ModelID,
				"upstreamStatus": upstreamStatus,
			},
		}
		if failure != "" {
			entry.Level = platform.LogLevelWarn
			entry.Status = platform.LogStatusFailed
			entry.Detail["error"] = failure
		}
		s.recordLog(request, entry)
	}()
	upstreamURL, err := runtimeLLMGeminiURL(target, request.PathValue("path"), request.URL.Query())
	if err != nil {
		failure = "凭据 API 地址无效"
		s.writeRuntimeLLMError(w, runtimeLLMProtocolGemini, http.StatusBadGateway, err.Error())
		return
	}
	request.Body = http.MaxBytesReader(w, request.Body, runtimeLLMMaxBodyBytes)
	upstreamRequest, err := http.NewRequestWithContext(request.Context(), request.Method, upstreamURL, request.Body)
	if err != nil {
		failure = "无法创建上游请求"
		s.writeRuntimeLLMError(w, runtimeLLMProtocolGemini, http.StatusBadGateway, "无法创建上游请求")
		return
	}
	upstreamRequest.Header.Set("Content-Type", request.Header.Get("Content-Type"))
	upstreamRequest.Header.Set("x-goog-api-key", target.Secret)
	response, err := s.runtimeLLMClient.Do(upstreamRequest)
	if err != nil {
		failure = "无法连接模型服务"
		s.writeRuntimeLLMError(w, runtimeLLMProtocolGemini, http.StatusBadGateway, "无法连接模型服务")
		return
	}
	defer response.Body.Close()
	upstreamStatus = response.StatusCode
	if response.StatusCode >= http.StatusBadRequest {
		failure = fmt.Sprintf("模型服务返回 HTTP %d", response.StatusCode)
	}
	copyRuntimeLLMHeaders(response.Header, w.Header())
	w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		w.Header().Set("X-Accel-Buffering", "no")
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(flushingWriter{ResponseWriter: w}, response.Body)
}

func (s *Server) runtimeLLMAnthropicCountTokens(w http.ResponseWriter, request *http.Request) {
	target, ok := s.resolveRuntimeLLMTarget(w, request, runtimeLLMProtocolAnthropic)
	if !ok {
		return
	}
	body, ok := readRuntimeLLMBody(w, request)
	if !ok {
		return
	}
	body, _, err := overrideRuntimeLLMModel(body, target.ModelID)
	if err != nil {
		s.writeRuntimeLLMError(w, runtimeLLMProtocolAnthropic, http.StatusBadRequest, "请求内容格式无效")
		return
	}
	upstreamProtocol, err := runtimeLLMProtocolForCredential(target.Protocol)
	if err != nil {
		s.writeRuntimeLLMError(w, runtimeLLMProtocolAnthropic, http.StatusBadRequest, err.Error())
		return
	}
	if upstreamProtocol != runtimeLLMProtocolAnthropic {
		claudeAdapter, _ := runtimeLLMConverter.Get(adapter.ProtocolClaudeMessages)
		params, report, err := claudeAdapter.DecodeRequest(body)
		if err != nil {
			s.writeRuntimeLLMError(w, runtimeLLMProtocolAnthropic, http.StatusBadRequest, "无法解析 Anthropic 请求")
			return
		}
		if upstreamAdapter, supported := upstreamProtocol.adapterProtocol(); supported {
			s.logRuntimeLLMReport(request, target, adapter.ProtocolClaudeMessages, upstreamAdapter, report)
		}
		s.writeJSON(w, http.StatusOK, map[string]int64{"input_tokens": token.EstimateRequestTokens(params)})
		return
	}
	upstreamURL, err := runtimeLLMUpstreamURL(target, true)
	if err != nil {
		s.writeRuntimeLLMError(w, runtimeLLMProtocolAnthropic, http.StatusBadGateway, err.Error())
		return
	}
	s.forwardRuntimeLLM(
		w, request, target, runtimeLLMProtocolAnthropic, upstreamProtocol,
		upstreamURL, body, body, false,
	)
}

func (s *Server) runtimeLLM(w http.ResponseWriter, request *http.Request, clientProtocol runtimeLLMProtocol) {
	target, ok := s.resolveRuntimeLLMTarget(w, request, clientProtocol)
	if !ok {
		return
	}
	body, ok := readRuntimeLLMBody(w, request)
	if !ok {
		return
	}
	body, streaming, err := overrideRuntimeLLMModel(body, target.ModelID)
	if err != nil {
		s.writeRuntimeLLMError(w, clientProtocol, http.StatusBadRequest, "请求内容格式无效")
		return
	}
	originalBody := body
	upstreamProtocol, err := runtimeLLMProtocolForCredential(target.Protocol)
	if err != nil {
		s.writeRuntimeLLMError(w, clientProtocol, http.StatusBadRequest, err.Error())
		return
	}
	var upstreamURL string
	if upstreamProtocol == runtimeLLMProtocolGemini {
		upstreamURL, err = runtimeLLMGeminiGenerateURL(target, streaming)
	} else {
		upstreamURL, err = runtimeLLMUpstreamURL(target, false)
	}
	if err != nil {
		s.writeRuntimeLLMError(w, clientProtocol, http.StatusBadGateway, err.Error())
		return
	}

	if clientProtocol != upstreamProtocol {
		converted, err := convertRuntimeLLMRequest(
			clientProtocol, upstreamProtocol, target.ModelID, body, streaming,
		)
		if err != nil {
			s.writeRuntimeLLMError(w, clientProtocol, http.StatusUnprocessableEntity, "请求无法转换为凭据所需的协议")
			return
		}
		s.setRuntimeLLMConversionHeaders(w.Header(), clientProtocol, upstreamProtocol)
		body = converted
	}
	s.forwardRuntimeLLM(
		w, request, target, clientProtocol, upstreamProtocol,
		upstreamURL, originalBody, body, streaming,
	)
}

func (s *Server) resolveRuntimeLLMTarget(
	w http.ResponseWriter, request *http.Request, protocol runtimeLLMProtocol,
) (platform.RuntimeLLMTarget, bool) {
	runtimeToken := runtimeLLMToken(request)
	if runtimeToken == "" {
		s.writeRuntimeLLMError(w, protocol, http.StatusUnauthorized, "缺少沙箱运行时令牌")
		return platform.RuntimeLLMTarget{}, false
	}
	target, err := s.store.ResolveRuntimeLLMTarget(
		request.Context(), request.PathValue("id"), request.PathValue("credentialId"), runtimeToken,
	)
	if errors.Is(err, store.ErrRuntimeUnauthorized) {
		s.writeRuntimeLLMError(w, protocol, http.StatusUnauthorized, "沙箱运行时令牌无效或已失效")
		return platform.RuntimeLLMTarget{}, false
	}
	if err != nil {
		s.logger.Error("resolve runtime LLM target failed", "error", err)
		s.writeRuntimeLLMError(w, protocol, http.StatusInternalServerError, "LLM 接入层暂时不可用")
		return platform.RuntimeLLMTarget{}, false
	}
	return target, true
}

func runtimeLLMToken(request *http.Request) string {
	if value := strings.TrimSpace(request.Header.Get("x-api-key")); value != "" {
		return value
	}
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	scheme, value, ok := strings.Cut(authorization, " ")
	if ok && strings.EqualFold(scheme, "Bearer") {
		return strings.TrimSpace(value)
	}
	if value := strings.TrimSpace(request.URL.Query().Get("key")); value != "" {
		return value
	}
	return ""
}

func readRuntimeLLMBody(w http.ResponseWriter, request *http.Request) ([]byte, bool) {
	request.Body = http.MaxBytesReader(w, request.Body, runtimeLLMMaxBodyBytes)
	body, err := io.ReadAll(request.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			http.Error(w, `{"error":"请求内容过大"}`, http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, `{"error":"无法读取请求内容"}`, http.StatusBadRequest)
		}
		return nil, false
	}
	return body, true
}

func overrideRuntimeLLMModel(body []byte, modelID string) ([]byte, bool, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false, err
	}
	payload["model"] = modelID
	streaming, _ := payload["stream"].(bool)
	result, err := json.Marshal(payload)
	return result, streaming, err
}

func runtimeLLMUpstreamURL(target platform.RuntimeLLMTarget, countTokens bool) (string, error) {
	base := strings.TrimSpace(target.Endpoint)
	if base == "" {
		switch target.Protocol {
		case "anthropic":
			base = "https://api.anthropic.com/v1"
		case "openai-responses", "openai-chat":
			base = "https://api.openai.com/v1"
		default:
			return "", errors.New("凭据没有配置可用的 API 地址")
		}
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("凭据 API 地址无效")
	}
	operation := ""
	switch target.Protocol {
	case "anthropic":
		operation = "/v1/messages"
		if countTokens {
			operation += "/count_tokens"
		}
	case "openai-responses":
		operation = "/v1/responses"
	case "openai-chat":
		operation = "/v1/chat/completions"
	default:
		return "", errors.New("凭据协议暂不支持")
	}
	path := strings.TrimRight(parsed.Path, "/")
	if countTokens && strings.HasSuffix(path, "/v1/messages") {
		parsed.Path = path + "/count_tokens"
		parsed.RawPath = ""
		return parsed.String(), nil
	}
	if !strings.HasSuffix(path, operation) {
		if strings.HasSuffix(path, "/v1") && strings.HasPrefix(operation, "/v1/") {
			path += strings.TrimPrefix(operation, "/v1")
		} else {
			path += operation
		}
	}
	parsed.Path = path
	parsed.RawPath = ""
	return parsed.String(), nil
}

func runtimeLLMGeminiURL(target platform.RuntimeLLMTarget, requestPath string, query url.Values) (string, error) {
	base := strings.TrimSpace(target.Endpoint)
	if base == "" {
		base = "https://generativelanguage.googleapis.com/v1beta"
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("凭据 API 地址无效")
	}
	path := "/" + strings.TrimLeft(requestPath, "/")
	basePath := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(basePath, "/v1beta") && strings.HasPrefix(path, "/v1beta/") {
		path = strings.TrimPrefix(path, "/v1beta")
	}
	parsed.Path = basePath + path
	parsed.RawPath = ""
	forwardQuery := make(url.Values, len(query))
	for key, values := range query {
		forwardQuery[key] = slices.Clone(values)
	}
	forwardQuery.Set("key", target.Secret)
	parsed.RawQuery = forwardQuery.Encode()
	return parsed.String(), nil
}

func runtimeLLMGeminiGenerateURL(target platform.RuntimeLLMTarget, streaming bool) (string, error) {
	base := strings.TrimSpace(target.Endpoint)
	if base == "" {
		base = "https://generativelanguage.googleapis.com/v1beta"
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("凭据 API 地址无效")
	}
	modelID := strings.TrimSpace(target.ModelID)
	if modelID == "" {
		return "", errors.New("凭据没有绑定可用模型")
	}
	operation := ":generateContent"
	if streaming {
		operation = ":streamGenerateContent"
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/models/" + modelID + operation
	parsed.RawPath = ""
	query := parsed.Query()
	if streaming {
		query.Set("alt", "sse")
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func runtimeLLMGeminiStreaming(requestPath string, query url.Values) bool {
	return strings.Contains(strings.ToLower(requestPath), ":streamgeneratecontent") ||
		strings.EqualFold(strings.TrimSpace(query.Get("alt")), "sse")
}

func (s *Server) forwardRuntimeLLM(
	w http.ResponseWriter,
	request *http.Request,
	target platform.RuntimeLLMTarget,
	clientProtocol, upstreamProtocol runtimeLLMProtocol,
	upstreamURL string,
	originalBody, body []byte,
	streaming bool,
) {
	translatedRequest := body
	ctx := request.Context()
	started := time.Now()
	var upstreamStatus int
	failure := ""
	// 记录 LLM 代理调用：只写元信息（凭据/协议/模型/状态码/耗时），
	// 绝不记录请求与响应 body；上游错误可能携带含密钥的 URL，只写通用原因。
	defer func() {
		entry := platform.LogEntry{
			Category: platform.LogCategoryLLM, Action: "proxy",
			Message:      "LLM 代理请求 " + target.ModelID,
			ResourceKind: "credential", ResourceID: target.CredentialID,
			DurationMS: time.Since(started).Milliseconds(),
			Detail: map[string]any{
				"sandboxId":      target.SandboxID,
				"provider":       target.ProviderID,
				"protocol":       string(clientProtocol) + "->" + string(upstreamProtocol),
				"model":          target.ModelID,
				"streaming":      streaming,
				"upstreamStatus": upstreamStatus,
			},
		}
		if failure != "" {
			entry.Level = platform.LogLevelWarn
			entry.Status = platform.LogStatusFailed
			entry.Detail["error"] = failure
		}
		s.recordLog(request, entry)
	}()
	if !streaming {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, runtimeLLMNonStreamingTimeout)
		defer cancel()
	}
	upstreamRequest, err := http.NewRequestWithContext(
		ctx, http.MethodPost, upstreamURL, bytes.NewReader(body),
	)
	if err != nil {
		failure = "无法创建上游请求"
		s.writeRuntimeLLMError(w, clientProtocol, http.StatusBadGateway, "无法创建上游请求")
		return
	}
	upstreamRequest.Header.Set("Content-Type", "application/json")
	upstreamRequest.Header.Set("Accept", "application/json")
	if streaming {
		upstreamRequest.Header.Set("Accept", "text/event-stream")
	}
	switch upstreamProtocol {
	case runtimeLLMProtocolAnthropic:
		upstreamRequest.Header.Set("x-api-key", target.Secret)
		version := strings.TrimSpace(request.Header.Get("anthropic-version"))
		if version == "" {
			version = "2023-06-01"
		}
		upstreamRequest.Header.Set("anthropic-version", version)
		if beta := strings.TrimSpace(request.Header.Get("anthropic-beta")); beta != "" {
			upstreamRequest.Header.Set("anthropic-beta", beta)
		}
	case runtimeLLMProtocolGemini:
		upstreamRequest.Header.Set("x-goog-api-key", target.Secret)
	default:
		upstreamRequest.Header.Set("Authorization", "Bearer "+target.Secret)
	}
	response, err := s.runtimeLLMClient.Do(upstreamRequest)
	if err != nil {
		s.logger.Warn("runtime LLM upstream request failed",
			"sandbox", target.SandboxID, "credential", target.CredentialID,
			"protocol", target.Protocol, "error", err)
		failure = "无法连接模型服务"
		s.writeRuntimeLLMError(w, clientProtocol, http.StatusBadGateway, "无法连接模型服务")
		return
	}
	defer response.Body.Close()
	copyRuntimeLLMHeaders(response.Header, w.Header())
	upstreamStatus = response.StatusCode
	if response.StatusCode >= http.StatusBadRequest {
		failure = fmt.Sprintf("模型服务返回 HTTP %d", response.StatusCode)
		s.forwardRuntimeLLMError(w, clientProtocol, upstreamProtocol, response)
		return
	}
	if streaming {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(response.StatusCode)
		if clientProtocol == upstreamProtocol {
			_, _ = io.Copy(flushingWriter{ResponseWriter: w}, response.Body)
			return
		}
		s.convertRuntimeLLMStream(
			w, request.Context(), response.Body, target, upstreamProtocol, clientProtocol,
			originalBody, body,
		)
		return
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, runtimeLLMMaxBodyBytes+1))
	if err != nil || len(responseBody) > runtimeLLMMaxBodyBytes {
		failure = "模型服务响应无效"
		s.writeRuntimeLLMError(w, clientProtocol, http.StatusBadGateway, "模型服务响应无效")
		return
	}
	if clientProtocol != upstreamProtocol {
		converted, err := convertRuntimeLLMResponse(
			ctx, upstreamProtocol, clientProtocol, target.ModelID,
			originalBody, translatedRequest, responseBody,
		)
		if err != nil {
			failure = "模型响应无法转换为 Agent 所需协议"
			s.writeRuntimeLLMError(w, clientProtocol, http.StatusBadGateway, "模型响应无法转换为 Agent 所需协议")
			return
		}
		responseBody = converted
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(responseBody)
}

func (s *Server) forwardRuntimeLLMError(
	w http.ResponseWriter, clientProtocol, upstreamProtocol runtimeLLMProtocol, response *http.Response,
) {
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		s.writeRuntimeLLMError(w, clientProtocol, http.StatusBadGateway, "无法读取模型服务错误")
		return
	}
	if clientProtocol != upstreamProtocol {
		upstreamAdapter, upstreamSupported := upstreamProtocol.adapterProtocol()
		clientAdapter, clientSupported := clientProtocol.adapterProtocol()
		if upstreamSupported && clientSupported {
			if converted, report, conversionErr := runtimeLLMConverter.ConvertError(
				upstreamAdapter, clientAdapter, body, response.StatusCode,
			); conversionErr == nil {
				s.setRuntimeLLMReportHeaders(w.Header(), upstreamAdapter, clientAdapter, report)
				body = converted
			}
		} else {
			body = runtimeLLMErrorBody(clientProtocol, response.StatusCode, runtimeLLMErrorMessage(body))
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
}

func (s *Server) convertRuntimeLLMStream(
	w http.ResponseWriter,
	ctx context.Context,
	reader io.Reader,
	target platform.RuntimeLLMTarget,
	from, to runtimeLLMProtocol,
	originalRequest, translatedRequest []byte,
) {
	fromAdapter, fromSupported := from.adapterProtocol()
	if to == runtimeLLMProtocolAnthropic && fromSupported && from != runtimeLLMProtocolAnthropic {
		s.convertRuntimeLLMToAnthropicStream(w, ctx, reader, target, fromAdapter)
		return
	}
	if from == runtimeLLMProtocolAnthropic && to == runtimeLLMProtocolResponses {
		s.convertRuntimeLLMToResponsesStream(
			w, ctx, reader, target,
			runtimeLLMResponseToolNameMap(to, originalRequest, translatedRequest),
		)
		return
	}
	if err := s.convertRuntimeLLMCLIProxyStream(
		flushingWriter{ResponseWriter: w}, ctx, reader, target.ModelID,
		from, to, originalRequest, translatedRequest,
	); err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Warn("runtime LLM stream conversion failed",
			"sandbox", target.SandboxID, "credential", target.CredentialID,
			"from", from, "to", to, "error", err)
		s.writeRuntimeLLMStreamError(w, to, "模型流格式转换失败")
	}
}

func (s *Server) writeRuntimeLLMStreamError(w http.ResponseWriter, protocol runtimeLLMProtocol, message string) {
	payload := map[string]any{"type": "error", "error": map[string]string{"type": "api_error", "message": message}}
	if protocol == runtimeLLMProtocolGemini {
		payload = map[string]any{"error": map[string]any{"code": http.StatusBadGateway, "message": message, "status": "UNKNOWN"}}
	}
	data, _ := json.Marshal(payload)
	event := &stream.SSEEvent{Data: string(data)}
	if protocol == runtimeLLMProtocolAnthropic {
		event.Event = "error"
	}
	_ = stream.NewSSEWriter(w).Write(event)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *Server) writeRuntimeLLMError(w http.ResponseWriter, protocol runtimeLLMProtocol, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(runtimeLLMErrorBody(protocol, status, message))
}

func (s *Server) setRuntimeLLMConversionHeaders(
	header http.Header, from, to runtimeLLMProtocol,
) {
	header.Set("X-AgentBox-Conversion", string(from)+"->"+string(to))
	header.Set("X-AgentBox-Conversion-Engine", "CLIProxyAPI/v6.7.53")
}

func (s *Server) setRuntimeLLMReportHeaders(
	header http.Header, from, to adapter.Protocol, report *adapter.Report,
) {
	if report == nil {
		return
	}
	if header.Get("X-AgentBox-Conversion") == "" {
		header.Set("X-AgentBox-Conversion", string(from)+"->"+string(to))
	}
	if fields := runtimeLLMReportFields(report); len(fields) > 0 {
		existing := strings.TrimSpace(header.Get("X-AgentBox-Conversion-Lost-Fields"))
		if existing != "" {
			fields = append(strings.Split(existing, ","), fields...)
			slices.Sort(fields)
		}
		header.Set("X-AgentBox-Conversion-Lost-Fields", strings.Join(fields, ","))
	}
	if len(report.Warnings) > 0 {
		header.Set("X-AgentBox-Conversion-Warnings", fmt.Sprintf("%d", len(report.Warnings)))
	}
}

func (s *Server) logRuntimeLLMReport(
	request *http.Request,
	target platform.RuntimeLLMTarget,
	from, to adapter.Protocol,
	report *adapter.Report,
) {
	if report == nil || (len(report.LostFields) == 0 && len(report.Warnings) == 0) {
		return
	}
	s.logger.WarnContext(request.Context(), "runtime LLM protocol conversion degraded",
		"sandbox", target.SandboxID, "credential", target.CredentialID,
		"from", from, "to", to, "lostFields", runtimeLLMReportFields(report),
		"warnings", len(report.Warnings))
}

func runtimeLLMReportFields(report *adapter.Report) []string {
	seen := make(map[string]struct{})
	for _, field := range report.LostFields {
		name := field.Source + "." + field.Field
		seen[name] = struct{}{}
	}
	fields := make([]string, 0, len(seen))
	for field := range seen {
		fields = append(fields, field)
	}
	slices.Sort(fields)
	return fields
}

func copyRuntimeLLMHeaders(source, target http.Header) {
	for key, values := range source {
		switch strings.ToLower(key) {
		case "connection", "content-length", "content-type", "keep-alive", "proxy-authenticate",
			"proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade", "set-cookie":
			continue
		}
		for _, value := range values {
			target.Add(key, value)
		}
	}
}

type flushingWriter struct {
	http.ResponseWriter
}

func (w flushingWriter) Write(data []byte) (int, error) {
	written, err := w.ResponseWriter.Write(data)
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
	return written, err
}

func newRuntimeLLMHTTPClient() *http.Client {
	allowPrivate := strings.EqualFold(os.Getenv("AGENTBOX_ALLOW_PRIVATE_PROVIDER_ENDPOINTS"), "true")
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			var lastError error
			for _, candidate := range addresses {
				ip := candidate.IP
				if !allowPrivate && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
					continue
				}
				connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if dialErr == nil {
					return connection, nil
				}
				lastError = dialErr
			}
			if lastError != nil {
				return nil, lastError
			}
			return nil, errors.New("模型接口解析到不允许访问的内网地址")
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 90 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("模型接口不允许重定向")
		},
	}
}

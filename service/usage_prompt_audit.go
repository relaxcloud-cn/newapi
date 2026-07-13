package service

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

const (
	UsagePromptAuditSchemaV1       = "newapi_prompt_audit:v1"
	UsagePromptAuditOtherKey       = "prompt_audit"
	maxUsagePromptAuditBodyBytes   = 256 * 1024
	maxUsagePromptAuditTextBytes   = 64 * 1024
	maxUsagePromptAuditItems       = 200
	usagePromptAuditClientSource   = "client_request"
	usagePromptAuditUpstreamSource = "upstream_request"
	usagePromptAuditResponseSource = "upstream_response"
)

type UsagePromptAudit struct {
	Schema           string                 `json:"schema"`
	UserInputs       []UsagePromptAuditText `json:"user_inputs,omitempty"`
	AssistantOutputs []UsagePromptAuditText `json:"assistant_outputs,omitempty"`
	SystemPrompts    []UsagePromptAuditText `json:"system_prompts,omitempty"`
	Skills           []UsagePromptAuditItem `json:"skills,omitempty"`
	Tools            []UsagePromptAuditItem `json:"tools,omitempty"`
	ToolCalls        []UsagePromptAuditItem `json:"tool_calls,omitempty"`
	Plugins          []UsagePromptAuditItem `json:"plugins,omitempty"`
	Counts           map[string]int         `json:"counts"`
}

type UsagePromptAuditText struct {
	Source string `json:"source"`
	Text   string `json:"text"`
}

type UsagePromptAuditItem struct {
	Source string `json:"source"`
	Type   string `json:"type,omitempty"`
	Name   string `json:"name,omitempty"`
	Value  any    `json:"value,omitempty"`
}

type UsagePromptAuditCapturedBodies struct {
	ClientRequestBody            []byte
	ClientRequestBodyTruncated   bool
	UpstreamRequestBody          []byte
	UpstreamRequestBodyTruncated bool
	ResponseBody                 []byte
	ResponseBodyTruncated        bool
}

func BuildUsagePromptAudit(clientRequestBody []byte, upstreamRequestBody []byte, responseBody []byte) *UsagePromptAudit {
	return BuildUsagePromptAuditFromCapturedBodies(UsagePromptAuditCapturedBodies{
		ClientRequestBody:   clientRequestBody,
		UpstreamRequestBody: upstreamRequestBody,
		ResponseBody:        responseBody,
	})
}

func BuildUsagePromptAuditFromCapturedBodies(bodies UsagePromptAuditCapturedBodies) *UsagePromptAudit {
	audit := &UsagePromptAudit{
		Schema: UsagePromptAuditSchemaV1,
		Counts: map[string]int{},
	}

	extractUsagePromptAuditRequest(audit, usagePromptAuditClientSource, bodies.ClientRequestBody)
	extractUsagePromptAuditRequest(audit, usagePromptAuditUpstreamSource, bodies.UpstreamRequestBody)
	extractUsagePromptAuditResponse(audit, usagePromptAuditResponseSource, bodies.ResponseBody)

	normalizeUsagePromptAuditAssistantOutputs(audit.AssistantOutputs)
	normalizeUsagePromptAudit(audit)

	if len(audit.UserInputs) == 0 &&
		len(audit.AssistantOutputs) == 0 &&
		len(audit.SystemPrompts) == 0 &&
		len(audit.Skills) == 0 &&
		len(audit.Tools) == 0 &&
		len(audit.ToolCalls) == 0 &&
		len(audit.Plugins) == 0 {
		return nil
	}

	return audit
}

func InjectUsagePromptAudit(other *model.LogOther, relayInfo *relaycommon.RelayInfo) {
	if other == nil || relayInfo == nil {
		return
	}
	audit := BuildUsagePromptAuditFromCapturedBodies(UsagePromptAuditCapturedBodies{
		ClientRequestBody:            relayInfo.UsageLogRawClientRequestBody,
		ClientRequestBodyTruncated:   relayInfo.UsageLogRawClientRequestBodyTruncated,
		UpstreamRequestBody:          relayInfo.UsageLogRawUpstreamRequestBody,
		UpstreamRequestBodyTruncated: relayInfo.UsageLogRawUpstreamRequestBodyTruncated,
		ResponseBody:                 relayInfo.UsageLogRawResponseBody,
		ResponseBodyTruncated:        relayInfo.UsageLogRawResponseBodyTruncated,
	})
	if audit == nil {
		return
	}
	other.SetAudit(UsagePromptAuditOtherKey, audit)
}

func NormalizeUsagePromptAuditOther(other string) string {
	if other == "" {
		return other
	}
	var otherMap map[string]any
	if err := common.Unmarshal([]byte(other), &otherMap); err != nil {
		return other
	}
	auditInfo, exists := otherMap["audit_info"].(map[string]any)
	if !exists {
		return other
	}
	rawAudit, exists := auditInfo[UsagePromptAuditOtherKey]
	if !exists {
		return other
	}
	auditData, err := common.Marshal(rawAudit)
	if err != nil {
		return other
	}
	var audit UsagePromptAudit
	if err = common.Unmarshal(auditData, &audit); err != nil || audit.Schema != UsagePromptAuditSchemaV1 {
		return other
	}
	normalizeUsagePromptAudit(&audit)
	auditInfo[UsagePromptAuditOtherKey] = audit
	normalized, err := common.Marshal(otherMap)
	if err != nil {
		return other
	}
	return string(normalized)
}

func normalizeUsagePromptAudit(audit *UsagePromptAudit) {
	if audit == nil {
		return
	}
	tools := make([]UsagePromptAuditItem, 0, len(audit.Tools))
	for _, tool := range audit.Tools {
		if item, ok := tool.Value.(map[string]any); ok &&
			strings.HasPrefix(auditString(item["description"]), "ClawOps ") &&
			strings.HasSuffix(auditString(item["description"]), " tool replay") {
			audit.ToolCalls = append(audit.ToolCalls, tool)
			continue
		}
		tools = append(tools, tool)
	}
	audit.Tools = tools
	audit.SystemPrompts = deduplicateUsagePromptAuditTexts(audit.SystemPrompts)
	audit.Skills = deduplicateUsagePromptAuditItems(audit.Skills)
	audit.Tools = deduplicateUsagePromptAuditItems(audit.Tools)
	audit.ToolCalls = deduplicateUsagePromptAuditIdentifiedToolCalls(audit.ToolCalls)
	audit.Plugins = deduplicateUsagePromptAuditItems(audit.Plugins)
	if audit.Counts == nil {
		audit.Counts = map[string]int{}
	}
	audit.Counts["user_inputs"] = len(audit.UserInputs)
	audit.Counts["assistant_outputs"] = len(audit.AssistantOutputs)
	audit.Counts["system_prompts"] = len(audit.SystemPrompts)
	audit.Counts["skills"] = len(audit.Skills)
	audit.Counts["tools"] = len(audit.Tools)
	audit.Counts["tool_calls"] = len(audit.ToolCalls)
	audit.Counts["plugins"] = len(audit.Plugins)
}

func normalizeUsagePromptAuditAssistantOutputs(outputs []UsagePromptAuditText) {
	for index := range outputs {
		var result map[string]any
		if err := common.Unmarshal([]byte(outputs[index].Text), &result); err != nil ||
			auditString(result["determination"]) == "" {
			continue
		}
		// ClawOps renders a triaged result's summary as the user-visible conclusion.
		for _, key := range []string{"summary", "headline", "customer_line", "customerLine"} {
			if text := auditString(result[key]); strings.TrimSpace(text) != "" {
				outputs[index].Text = text
				break
			}
		}
	}

}

func CaptureUsagePromptAuditBytes(body []byte) ([]byte, bool) {
	if len(body) == 0 {
		return nil, false
	}
	if len(body) <= maxUsagePromptAuditBodyBytes {
		captured := make([]byte, len(body))
		copy(captured, body)
		return captured, false
	}
	captured := make([]byte, maxUsagePromptAuditBodyBytes)
	copy(captured, body[:maxUsagePromptAuditBodyBytes])
	return captured, true
}

func CaptureUsagePromptAuditBodyFromStorage(storage common.BodyStorage) ([]byte, bool, error) {
	if storage == nil || storage.Size() == 0 {
		return nil, false, nil
	}
	if _, err := storage.Seek(0, io.SeekStart); err != nil {
		return nil, false, err
	}
	data, err := io.ReadAll(io.LimitReader(storage, int64(maxUsagePromptAuditBodyBytes)+1))
	if seekErr := func() error {
		_, seekErr := storage.Seek(0, io.SeekStart)
		return seekErr
	}(); seekErr != nil && err == nil {
		err = seekErr
	}
	if err != nil {
		return nil, false, err
	}
	if len(data) > maxUsagePromptAuditBodyBytes {
		captured := make([]byte, maxUsagePromptAuditBodyBytes)
		copy(captured, data[:maxUsagePromptAuditBodyBytes])
		return captured, true, nil
	}
	return data, false, nil
}

func extractUsagePromptAuditRequest(audit *UsagePromptAudit, source string, body []byte) {
	root := usagePromptAuditObject(body)
	if len(root) == 0 {
		return
	}

	extractUsagePromptAuditMessages(audit, source, root)
	extractUsagePromptAuditInput(audit, source, root)
	extractUsagePromptAuditSystemPrompts(audit, source, root)
	extractUsagePromptAuditTools(audit, source, root)
	extractUsagePromptAuditMetadata(audit, source, root)
	extractUsagePromptAuditClawOpsProfile(audit, source, root)
	extractUsagePromptAuditClawOpsDispatch(audit, source, root)
}

func extractUsagePromptAuditResponse(audit *UsagePromptAudit, source string, body []byte) {
	root := usagePromptAuditObject(body)
	if len(root) == 0 {
		return
	}

	choices, ok := root["choices"].([]any)
	if ok {
		for i, choiceValue := range choices {
			choice, ok := choiceValue.(map[string]any)
			if !ok {
				continue
			}
			message, ok := choice["message"].(map[string]any)
			if !ok {
				continue
			}
			extractUsagePromptAuditContentTexts(&audit.AssistantOutputs, fmt.Sprintf("%s.choices[%d].message.content", source, i), message["content"])
			extractUsagePromptAuditToolCalls(audit, fmt.Sprintf("%s.choices[%d].message.tool_calls", source, i), message["tool_calls"])
		}
	}

	outputs, ok := root["output"].([]any)
	if ok {
		for i, outputValue := range outputs {
			output, ok := outputValue.(map[string]any)
			if !ok {
				continue
			}
			outputType := auditString(output["type"])
			switch outputType {
			case "message":
				if role := auditString(output["role"]); role == "" || role == "assistant" {
					extractUsagePromptAuditContentTexts(&audit.AssistantOutputs, fmt.Sprintf("%s.output[%d].content", source, i), output["content"])
				}
			case "function_call", "tool_call", "web_search_call", "file_search_call":
				appendUsagePromptAuditItem(&audit.ToolCalls, fmt.Sprintf("%s.output[%d]", source, i), output)
			default:
				if strings.Contains(outputType, "call") {
					appendUsagePromptAuditItem(&audit.ToolCalls, fmt.Sprintf("%s.output[%d]", source, i), output)
				}
			}
		}
	}

	if tools, ok := root["tools"].([]any); ok {
		for i, tool := range tools {
			appendUsagePromptAuditItem(&audit.ToolCalls, fmt.Sprintf("%s.tools[%d]", source, i), tool)
		}
	}
	if role := auditString(root["role"]); role == "" || role == "assistant" {
		extractUsagePromptAuditAnthropicResponseTexts(audit, source+".content", root["content"])
		if completion := auditString(root["completion"]); completion != "" {
			appendUsagePromptAuditText(&audit.AssistantOutputs, source+".completion", completion)
		}
	}
	extractUsagePromptAuditAnthropicContentTools(audit, source+".content", root["content"])
	extractUsagePromptAuditResponseMessages(audit, source, root)
	extractUsagePromptAuditClawOpsInvestigation(audit, source, root)
}

func extractUsagePromptAuditMessages(audit *UsagePromptAudit, source string, root map[string]any) {
	messages, ok := root["messages"].([]any)
	if !ok {
		return
	}
	for i, messageValue := range messages {
		message, ok := messageValue.(map[string]any)
		if !ok {
			continue
		}
		role := auditString(message["role"])
		contentPath := fmt.Sprintf("%s.messages[%d].content", source, i)
		switch role {
		case "system", "developer":
			extractUsagePromptAuditContentTexts(&audit.SystemPrompts, contentPath, message["content"])
		case "user":
			extractUsagePromptAuditContentTexts(&audit.UserInputs, contentPath, message["content"])
		}
		extractUsagePromptAuditToolCalls(audit, fmt.Sprintf("%s.messages[%d].tool_calls", source, i), message["tool_calls"])
		extractUsagePromptAuditAnthropicContentTools(audit, contentPath, message["content"])
		extractUsagePromptAuditComposerInputItems(audit, fmt.Sprintf("%s.messages[%d].composer", source, i), message["composer"])
	}
}

func extractUsagePromptAuditResponseMessages(audit *UsagePromptAudit, source string, root map[string]any) {
	messages, ok := root["messages"].([]any)
	if !ok {
		return
	}
	for i, messageValue := range messages {
		message, ok := messageValue.(map[string]any)
		if !ok {
			continue
		}
		if role := auditString(message["role"]); role == "" || role == "assistant" {
			extractUsagePromptAuditContentTexts(&audit.AssistantOutputs, fmt.Sprintf("%s.messages[%d].content", source, i), message["content"])
		}
		extractUsagePromptAuditToolCalls(audit, fmt.Sprintf("%s.messages[%d].tool_calls", source, i), message["tool_calls"])
	}
}

func extractUsagePromptAuditInput(audit *UsagePromptAudit, source string, root map[string]any) {
	input, exists := root["input"]
	if !exists {
		return
	}

	switch value := input.(type) {
	case string:
		appendUsagePromptAuditText(&audit.UserInputs, source+".input", value)
	case []any:
		for i, itemValue := range value {
			itemPath := fmt.Sprintf("%s.input[%d]", source, i)
			item, ok := itemValue.(map[string]any)
			if !ok {
				appendUsagePromptAuditText(&audit.UserInputs, itemPath, auditTextValue(itemValue))
				continue
			}
			itemType := auditString(item["type"])
			switch itemType {
			case "skill":
				appendUsagePromptAuditItem(&audit.Skills, itemPath, item)
			case "plugin":
				appendUsagePromptAuditItem(&audit.Plugins, itemPath, item)
			case "input_text":
				appendUsagePromptAuditText(&audit.UserInputs, itemPath+".text", auditString(item["text"]))
			case "message":
				if role := auditString(item["role"]); role == "" || role == "user" {
					extractUsagePromptAuditContentTexts(&audit.UserInputs, itemPath+".content", item["content"])
				}
			case "function_call", "function_call_output", "tool_call", "tool_result", "web_search_call", "file_search_call":
				appendUsagePromptAuditItem(&audit.ToolCalls, itemPath, item)
			default:
				if role := auditString(item["role"]); role == "user" {
					extractUsagePromptAuditContentTexts(&audit.UserInputs, itemPath+".content", item["content"])
				} else if strings.Contains(itemType, "call") || strings.Contains(itemType, "tool") {
					appendUsagePromptAuditItem(&audit.ToolCalls, itemPath, item)
				} else if text := auditString(item["text"]); text != "" {
					appendUsagePromptAuditText(&audit.UserInputs, itemPath+".text", text)
				}
			}
		}
	case map[string]any:
		if role := auditString(value["role"]); role == "" || role == "user" {
			extractUsagePromptAuditContentTexts(&audit.UserInputs, source+".input.content", value["content"])
		}
		if text := auditString(value["text"]); text != "" {
			appendUsagePromptAuditText(&audit.UserInputs, source+".input.text", text)
		}
	}
}

func extractUsagePromptAuditSystemPrompts(audit *UsagePromptAudit, source string, root map[string]any) {
	if system, exists := root["system"]; exists {
		extractUsagePromptAuditContentTexts(&audit.SystemPrompts, source+".system", system)
	}
	for _, key := range []string{"instructions", "instruction", "developerInstructions", "baseInstructions", "system_prompt", "systemPrompt"} {
		value, exists := root[key]
		if !exists {
			continue
		}
		appendUsagePromptAuditText(&audit.SystemPrompts, source+"."+key, auditTextValue(value))
	}
}

func extractUsagePromptAuditTools(audit *UsagePromptAudit, source string, root map[string]any) {
	if tools, ok := root["tools"].([]any); ok {
		for i, tool := range tools {
			appendUsagePromptAuditItem(&audit.Tools, fmt.Sprintf("%s.tools[%d]", source, i), tool)
		}
	}
	if functions, ok := root["functions"].([]any); ok {
		for i, function := range functions {
			appendUsagePromptAuditItem(&audit.Tools, fmt.Sprintf("%s.functions[%d]", source, i), function)
		}
	}
	if servers, ok := root["mcp_servers"].([]any); ok {
		for i, serverValue := range servers {
			server, ok := serverValue.(map[string]any)
			if !ok {
				continue
			}
			safeServer := map[string]any{
				"type": "mcp_server",
			}
			for _, key := range []string{"name", "url", "server_label"} {
				value := auditString(server[key])
				if key == "url" {
					value = sanitizeUsagePromptAuditURL(value)
				}
				if value != "" {
					safeServer[key] = value
				}
			}
			if transport := auditString(server["type"]); transport != "" {
				safeServer["transport"] = transport
			}
			appendUsagePromptAuditItem(&audit.Tools, fmt.Sprintf("%s.mcp_servers[%d]", source, i), safeServer)
		}
	}
}

func extractUsagePromptAuditAnthropicResponseTexts(audit *UsagePromptAudit, path string, value any) {
	switch content := value.(type) {
	case string:
		appendUsagePromptAuditText(&audit.AssistantOutputs, path, content)
	case []any:
		for i, partValue := range content {
			part, ok := partValue.(map[string]any)
			if !ok {
				continue
			}
			partType := auditString(part["type"])
			if partType != "" && partType != "text" {
				continue
			}
			if text := auditString(part["text"]); text != "" {
				appendUsagePromptAuditText(&audit.AssistantOutputs, fmt.Sprintf("%s[%d].text", path, i), text)
			}
		}
	}
}

func sanitizeUsagePromptAuditURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func extractUsagePromptAuditAnthropicContentTools(audit *UsagePromptAudit, path string, value any) {
	content, ok := value.([]any)
	if !ok {
		return
	}
	for i, partValue := range content {
		part, ok := partValue.(map[string]any)
		if !ok {
			continue
		}
		partType := auditString(part["type"])
		if partType == "tool_use" || partType == "tool_result" ||
			strings.HasSuffix(partType, "_tool_use") || strings.HasSuffix(partType, "_tool_result") {
			appendUsagePromptAuditItem(&audit.ToolCalls, fmt.Sprintf("%s[%d]", path, i), part)
		}
	}
}

func extractUsagePromptAuditMetadata(audit *UsagePromptAudit, source string, root map[string]any) {
	metadata, ok := root["metadata"].(map[string]any)
	if !ok {
		return
	}
	for _, key := range []string{"skills", "skill", "required_skills"} {
		extractUsagePromptAuditNamedValues(&audit.Skills, source+".metadata."+key, metadata[key])
	}
	for _, key := range []string{"plugins", "plugin", "required_plugins"} {
		extractUsagePromptAuditNamedValues(&audit.Plugins, source+".metadata."+key, metadata[key])
	}
}

func extractUsagePromptAuditComposerInputItems(audit *UsagePromptAudit, path string, value any) {
	composer, ok := value.(map[string]any)
	if !ok {
		return
	}
	inputItems, ok := composer["inputItems"].([]any)
	if !ok {
		return
	}
	for i, inputItemValue := range inputItems {
		itemPath := fmt.Sprintf("%s.inputItems[%d]", path, i)
		item, ok := inputItemValue.(map[string]any)
		if !ok {
			continue
		}
		if metadata, ok := item["metadata"].(map[string]any); ok {
			extractUsagePromptAuditNamedValues(&audit.Skills, itemPath+".metadata.capability_skills", metadata["capability_skills"])
			extractUsagePromptAuditNamedValues(&audit.Skills, itemPath+".metadata.skills", metadata["skills"])
			extractUsagePromptAuditNamedValues(&audit.Plugins, itemPath+".metadata.required_plugins", metadata["required_plugins"])
			extractUsagePromptAuditNamedValues(&audit.Plugins, itemPath+".metadata.plugins", metadata["plugins"])
		}
		if pathValue := auditString(item["path"]); pathValue != "" {
			appendUsagePromptAuditItem(&audit.Plugins, itemPath, map[string]any{
				"type":  "plugin",
				"name":  pathValue,
				"label": item["name"],
				"value": item,
			})
		}
	}
}

func extractUsagePromptAuditClawOpsProfile(audit *UsagePromptAudit, source string, root map[string]any) {
	profile, ok := root["profile"].(map[string]any)
	if !ok {
		return
	}
	extractUsagePromptAuditNamedValues(&audit.Skills, source+".profile.entry_skill", profile["entry_skill"])
	extractUsagePromptAuditNamedValues(&audit.Skills, source+".profile.skills", profile["skills"])
	extractUsagePromptAuditNamedValues(&audit.Plugins, source+".profile.plugin", profile["plugin"])
	extractUsagePromptAuditNamedValues(&audit.Plugins, source+".profile.required_plugins", profile["required_plugins"])
}

func extractUsagePromptAuditClawOpsDispatch(audit *UsagePromptAudit, source string, root map[string]any) {
	unit, ok := root["unit"].(map[string]any)
	if !ok {
		return
	}
	dispatch, ok := unit["dispatch"].(map[string]any)
	if !ok {
		return
	}
	steps, ok := dispatch["steps"].([]any)
	if !ok {
		return
	}
	for i, stepValue := range steps {
		step, ok := stepValue.(map[string]any)
		if !ok {
			continue
		}
		extractUsagePromptAuditNamedValues(&audit.Skills, fmt.Sprintf("%s.unit.dispatch.steps[%d].skills", source, i), step["skills"])
	}
}

func extractUsagePromptAuditClawOpsInvestigation(audit *UsagePromptAudit, source string, root map[string]any) {
	if customerLine := auditString(root["customer_line"]); customerLine != "" {
		appendUsagePromptAuditText(&audit.AssistantOutputs, source+".customer_line", customerLine)
	}
	if headline := auditString(root["headline"]); headline != "" {
		appendUsagePromptAuditText(&audit.AssistantOutputs, source+".headline", headline)
	}
	investigation, ok := root["investigation"].(map[string]any)
	if !ok {
		return
	}
	stages, ok := investigation["stages"].([]any)
	if !ok {
		return
	}
	for i, stageValue := range stages {
		stage, ok := stageValue.(map[string]any)
		if !ok {
			continue
		}
		stagePath := fmt.Sprintf("%s.investigation.stages[%d]", source, i)
		appendUsagePromptAuditText(&audit.AssistantOutputs, stagePath+".output", auditString(stage["output"]))
		extractUsagePromptAuditNamedValues(&audit.Skills, stagePath+".skill", stage["skill"])
		if tools, ok := stage["tools"].([]any); ok {
			for j, tool := range tools {
				appendUsagePromptAuditItem(&audit.ToolCalls, fmt.Sprintf("%s.tools[%d]", stagePath, j), tool)
			}
		}
	}
}

func extractUsagePromptAuditNamedValues(target *[]UsagePromptAuditItem, path string, value any) {
	switch values := value.(type) {
	case nil:
		return
	case []any:
		for i, item := range values {
			appendUsagePromptAuditItem(target, fmt.Sprintf("%s[%d]", path, i), item)
		}
	default:
		appendUsagePromptAuditItem(target, path, values)
	}
}

func extractUsagePromptAuditToolCalls(audit *UsagePromptAudit, path string, value any) {
	switch calls := value.(type) {
	case nil:
		return
	case []any:
		for i, call := range calls {
			appendUsagePromptAuditItem(&audit.ToolCalls, fmt.Sprintf("%s[%d]", path, i), call)
		}
	default:
		appendUsagePromptAuditItem(&audit.ToolCalls, path, calls)
	}
}

func extractUsagePromptAuditContentTexts(target *[]UsagePromptAuditText, path string, value any) {
	switch content := value.(type) {
	case nil:
		return
	case string:
		appendUsagePromptAuditText(target, path, content)
	case []any:
		for i, partValue := range content {
			partPath := fmt.Sprintf("%s[%d]", path, i)
			part, ok := partValue.(map[string]any)
			if !ok {
				appendUsagePromptAuditText(target, partPath, auditTextValue(partValue))
				continue
			}
			if text := auditString(part["text"]); text != "" {
				appendUsagePromptAuditText(target, partPath+".text", text)
				continue
			}
			if text := auditString(part["input_text"]); text != "" {
				appendUsagePromptAuditText(target, partPath+".input_text", text)
				continue
			}
			if text := auditString(part["output_text"]); text != "" {
				appendUsagePromptAuditText(target, partPath+".output_text", text)
			}
		}
	case map[string]any:
		if text := auditString(content["text"]); text != "" {
			appendUsagePromptAuditText(target, path+".text", text)
			return
		}
		appendUsagePromptAuditText(target, path, auditTextValue(content))
	}
}

func appendUsagePromptAuditText(target *[]UsagePromptAuditText, source string, text string) {
	if strings.TrimSpace(text) == "" || len(*target) >= maxUsagePromptAuditItems {
		return
	}
	if len(text) > maxUsagePromptAuditTextBytes {
		text = text[:maxUsagePromptAuditTextBytes]
	}
	*target = append(*target, UsagePromptAuditText{
		Source: source,
		Text:   text,
	})
}

func appendUsagePromptAuditItem(target *[]UsagePromptAuditItem, source string, value any) {
	if value == nil || len(*target) >= maxUsagePromptAuditItems {
		return
	}
	*target = append(*target, UsagePromptAuditItem{
		Source: source,
		Type:   auditItemType(value),
		Name:   auditItemName(value),
		Value:  value,
	})
}

func deduplicateUsagePromptAuditTexts(items []UsagePromptAuditText) []UsagePromptAuditText {
	seen := make(map[string]struct{}, len(items))
	deduplicated := make([]UsagePromptAuditText, 0, len(items))
	for _, item := range items {
		if _, exists := seen[item.Text]; exists {
			continue
		}
		seen[item.Text] = struct{}{}
		deduplicated = append(deduplicated, item)
	}
	return deduplicated
}

func deduplicateUsagePromptAuditItems(items []UsagePromptAuditItem) []UsagePromptAuditItem {
	seen := make(map[string]struct{}, len(items))
	deduplicated := make([]UsagePromptAuditItem, 0, len(items))
	for _, item := range items {
		identity, err := common.Marshal(struct {
			Type  string `json:"type,omitempty"`
			Name  string `json:"name,omitempty"`
			Value any    `json:"value,omitempty"`
		}{
			Type:  item.Type,
			Name:  item.Name,
			Value: item.Value,
		})
		if err != nil {
			deduplicated = append(deduplicated, item)
			continue
		}
		key := string(identity)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		deduplicated = append(deduplicated, item)
	}
	return deduplicated
}

func deduplicateUsagePromptAuditIdentifiedToolCalls(items []UsagePromptAuditItem) []UsagePromptAuditItem {
	seen := make(map[string]struct{}, len(items))
	deduplicated := make([]UsagePromptAuditItem, 0, len(items))
	for _, item := range items {
		value, ok := item.Value.(map[string]any)
		if !ok {
			deduplicated = append(deduplicated, item)
			continue
		}
		identifier := ""
		for _, key := range []string{"id", "call_id", "tool_use_id"} {
			if identifier = auditString(value[key]); identifier != "" {
				break
			}
		}
		if identifier == "" {
			deduplicated = append(deduplicated, item)
			continue
		}
		key := item.Type + "\x00" + identifier
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		deduplicated = append(deduplicated, item)
	}
	return deduplicated
}

func usagePromptAuditObject(body []byte) map[string]any {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil
	}
	var root map[string]any
	if err := common.Unmarshal(body, &root); err != nil {
		return nil
	}
	return root
}

func auditItemType(value any) string {
	if item, ok := value.(map[string]any); ok {
		return auditString(item["type"])
	}
	return ""
}

func auditItemName(value any) string {
	switch item := value.(type) {
	case string:
		return item
	case map[string]any:
		if name := auditString(item["name"]); name != "" {
			return name
		}
		if function, ok := item["function"].(map[string]any); ok {
			if name := auditString(function["name"]); name != "" {
				return name
			}
		}
		if name := auditString(item["server_label"]); name != "" {
			return name
		}
		if name := auditString(item["plugin"]); name != "" {
			return name
		}
		if name := auditString(item["call_id"]); name != "" {
			return name
		}
		if name := auditString(item["id"]); name != "" {
			return name
		}
		if name := auditString(item["tool_use_id"]); name != "" {
			return name
		}
		return auditString(item["type"])
	default:
		return auditTextValue(value)
	}
}

func auditTextValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		data, err := common.Marshal(value)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(data)
	}
}

func auditString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return auditTextValue(value)
}

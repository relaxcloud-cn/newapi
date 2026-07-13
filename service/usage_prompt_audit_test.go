package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildUsagePromptAuditExtractsChatRequestAndResponse(t *testing.T) {
	clientRequest := []byte(`{
		"model":"gpt-4.1",
		"messages":[
			{"role":"system","content":"client system prompt"},
			{"role":"user","content":[{"type":"text","text":"分析 8.8.8.8 的威胁情报"}]}
		],
		"tools":[{"type":"function","function":{"name":"lookup_ip","description":"查 IP 情报"}}],
		"metadata":{"plugins":["clawops-ip"],"skills":["ip-analysis"]}
	}`)
	upstreamRequest := []byte(`{
		"model":"gpt-4.1",
		"messages":[
			{"role":"developer","content":"newapi channel system prompt"},
			{"role":"user","content":"分析 8.8.8.8 的威胁情报"}
		],
		"tools":[{"type":"function","function":{"name":"lookup_ip"}}]
	}`)
	responseBody := []byte(`{
		"choices":[{"message":{
			"role":"assistant",
			"content":"8.8.8.8 是 Google Public DNS，未见直接恶意结论。",
			"tool_calls":[{"id":"call_lookup_ip","type":"function","function":{"name":"lookup_ip","arguments":"{\"ip\":\"8.8.8.8\"}"}}]
		}}]
	}`)

	audit := BuildUsagePromptAudit(clientRequest, upstreamRequest, responseBody)

	require.NotNil(t, audit)
	assert.Equal(t, UsagePromptAuditSchemaV1, audit.Schema)
	assertAuditDoesNotStoreRawBodies(t, audit)

	assertAuditTextContains(t, audit.UserInputs, "client_request.messages[1].content[0].text", "分析 8.8.8.8")
	assertAuditTextContains(t, audit.UserInputs, "upstream_request.messages[1].content", "分析 8.8.8.8")
	assertAuditTextContains(t, audit.SystemPrompts, "client_request.messages[0].content", "client system prompt")
	assertAuditTextContains(t, audit.SystemPrompts, "upstream_request.messages[0].content", "newapi channel system prompt")
	assertAuditTextContains(t, audit.AssistantOutputs, "upstream_response.choices[0].message.content", "Google Public DNS")
	assertAuditItemNamed(t, audit.Tools, "client_request.tools[0]", "lookup_ip")
	assertAuditItemNamed(t, audit.Tools, "upstream_request.tools[0]", "lookup_ip")
	assertAuditItemNamed(t, audit.Plugins, "client_request.metadata.plugins[0]", "clawops-ip")
	assertAuditItemNamed(t, audit.Skills, "client_request.metadata.skills[0]", "ip-analysis")
	assert.Len(t, audit.ToolCalls, 1)
	assertAuditItemNamed(t, audit.ToolCalls, "upstream_response.choices[0].message.tool_calls[0]", "lookup_ip")
	assert.Equal(t, 2, audit.Counts["user_inputs"])
	assert.Equal(t, 2, audit.Counts["system_prompts"])
	assert.Equal(t, 1, audit.Counts["assistant_outputs"])
	assert.Equal(t, 1, audit.Counts["tool_calls"])
}

func TestBuildUsagePromptAuditDoesNotStoreTopLevelPrompt(t *testing.T) {
	clientRequest := []byte(`{
		"model":"codex-mini",
		"instructions":"基础系统提示词",
		"developerInstructions":"开发者提示词",
		"input":[
			{"type":"skill","name":"claw-soc:soc-triage","path":"/skills/soc/SKILL.md"},
			{"type":"plugin","name":"claw-cigs"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"处理 CIGS 告警 651 并给出分诊结论"}]},
			{"role":"user","content":"综合研判最近 24 小时攻击态势"}
		],
		"tools":[{"type":"function","name":"run_triage"},{"type":"mcp","server_label":"cigs"}],
		"metadata":{"required_plugins":["claw-soc","claw-cigs"],"skills":[{"name":"triage-skill"}]},
		"prompt":{"id":"pmpt_123","variables":{"workspace":"default"}}
	}`)
	upstreamRequest := []byte(`{
		"model":"codex-mini",
		"input":"处理 CIGS 告警 651 并给出分诊结论",
		"tools":[{"type":"function","name":"run_triage"}]
	}`)
	responseBody := []byte(`{
		"output":[
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"分诊成功，告警 651 建议升级为中危。"}]},
			{"type":"function_call","name":"run_triage","arguments":{"alert_id":651}}
		]
	}`)

	audit := BuildUsagePromptAudit(clientRequest, upstreamRequest, responseBody)

	require.NotNil(t, audit)
	assertAuditTextContains(t, audit.SystemPrompts, "client_request.instructions", "基础系统提示词")
	assertAuditTextContains(t, audit.SystemPrompts, "client_request.developerInstructions", "开发者提示词")
	assertAuditTextContains(t, audit.UserInputs, "client_request.input[2].content[0].text", "CIGS 告警 651")
	assertAuditTextContains(t, audit.UserInputs, "client_request.input[3].content", "最近 24 小时")
	assertAuditTextContains(t, audit.UserInputs, "upstream_request.input", "CIGS 告警 651")
	assertAuditTextContains(t, audit.AssistantOutputs, "upstream_response.output[0].content[0].text", "分诊成功")
	assertAuditItemNamed(t, audit.Skills, "client_request.input[0]", "claw-soc:soc-triage")
	assertAuditItemNamed(t, audit.Skills, "client_request.metadata.skills[0]", "triage-skill")
	assertAuditItemNamed(t, audit.Tools, "client_request.tools[0]", "run_triage")
	assertAuditItemNamed(t, audit.Tools, "client_request.tools[1]", "cigs")
	assertAuditItemNamed(t, audit.ToolCalls, "upstream_response.output[1]", "run_triage")
	assertAuditItemNamed(t, audit.Plugins, "client_request.input[1]", "claw-cigs")
	assertAuditItemNamed(t, audit.Plugins, "client_request.metadata.required_plugins[0]", "claw-soc")
	assertAuditItemNamed(t, audit.Plugins, "client_request.metadata.required_plugins[1]", "claw-cigs")

	data, err := common.Marshal(audit)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "\"prompts\"")
	assert.NotContains(t, string(data), "pmpt_123")
}

func TestBuildUsagePromptAuditExtractsAnthropicRequestAndResponse(t *testing.T) {
	clientRequest := []byte(`{
		"model":"claude-sonnet-4-5",
		"system":[{"type":"text","text":"ClawOps Anthropic 系统提示词"}],
		"messages":[{
			"role":"user",
			"content":[
				{"type":"text","text":"分析 103.195.103.66"},
				{"type":"tool_result","tool_use_id":"toolu_lookup","content":"本地告警命中 10.35.177.119"}
			]
		}],
		"tools":[{"name":"lookup_ip","description":"查询 IP 情报","input_schema":{"type":"object"}}],
		"mcp_servers":[{
			"type":"url",
			"name":"cigs",
			"url":"https://audit-user:audit-password@mcp.example.test/sse?token=query-secret",
			"authorization_token":"must-not-be-recorded"
		}],
		"metadata":{"skills":["claw-cybersec-analysis:ip-analysis"],"plugins":["claw-cigs"]}
	}`)
	upstreamRequest := []byte(`{
		"model":"claude-sonnet-4-5",
		"system":"渠道追加的系统提示词",
		"messages":[{"role":"user","content":"分析 103.195.103.66"}],
		"tools":[{"name":"lookup_ip","input_schema":{"type":"object"}}]
	}`)
	responseBody := []byte(`{
		"id":"msg_123",
		"type":"message",
		"role":"assistant",
		"content":[
			{"type":"thinking","thinking":"隐藏推理不得进入使用记录","text":"thinking-text-must-not-be-recorded"},
			{"type":"text","text":"该 IP 与内网主机存在隧道告警关联。"},
			{"type":"tool_use","id":"toolu_cigs","name":"query_cigs","input":{"ip":"103.195.103.66"}},
			{"type":"web_search_tool_result","tool_use_id":"toolu_web","content":[{"type":"web_search_result","url":"https://example.test"}]}
		]
	}`)

	audit := BuildUsagePromptAudit(clientRequest, upstreamRequest, responseBody)

	require.NotNil(t, audit)
	assertAuditTextContains(t, audit.UserInputs, "client_request.messages[0].content[0].text", "103.195.103.66")
	assertAuditTextContains(t, audit.SystemPrompts, "client_request.system[0].text", "ClawOps Anthropic 系统提示词")
	assertAuditTextContains(t, audit.SystemPrompts, "upstream_request.system", "渠道追加的系统提示词")
	assertAuditTextContains(t, audit.AssistantOutputs, "upstream_response.content[1].text", "隧道告警关联")
	assertAuditItemNamed(t, audit.Tools, "client_request.tools[0]", "lookup_ip")
	assertAuditItemNamed(t, audit.Tools, "client_request.mcp_servers[0]", "cigs")
	assertAuditItemNamed(t, audit.ToolCalls, "client_request.messages[0].content[1]", "toolu_lookup")
	assertAuditItemNamed(t, audit.ToolCalls, "upstream_response.content[2]", "query_cigs")
	assertAuditItemNamed(t, audit.ToolCalls, "upstream_response.content[3]", "toolu_web")
	assert.Len(t, audit.ToolCalls, 3)
	assertAuditItemNamed(t, audit.Skills, "client_request.metadata.skills[0]", "claw-cybersec-analysis:ip-analysis")
	assertAuditItemNamed(t, audit.Plugins, "client_request.metadata.plugins[0]", "claw-cigs")

	data, err := common.Marshal(audit)
	require.NoError(t, err)
	jsonText := string(data)
	assert.NotContains(t, jsonText, "must-not-be-recorded")
	assert.NotContains(t, jsonText, "audit-user")
	assert.NotContains(t, jsonText, "audit-password")
	assert.NotContains(t, jsonText, "query-secret")
	assert.NotContains(t, jsonText, "隐藏推理不得进入使用记录")
	assert.NotContains(t, jsonText, "thinking-text-must-not-be-recorded")
	assert.Contains(t, jsonText, "https://mcp.example.test/sse")
}

func TestBuildUsagePromptAuditExtractsClawOpsRawSnapshotAndTriageResult(t *testing.T) {
	clientRequest := []byte(`{
		"messages":[
			{
				"role":"user",
				"content":"分析 103.195.103.66",
				"composer":{
					"inputItems":[
						{
							"type":"mention",
							"name":"IP 分析",
							"path":"agent-app://ip",
							"metadata":{
								"capability_skills":["claw-cybersec-analysis:ip-analysis"]
							}
						}
					]
				}
			}
		],
		"profile":{
			"entry_skill":"claw-soc:soc-triage",
			"plugin":"claw-soc",
			"required_plugins":["claw-cigs"]
		},
		"unit":{
			"dispatch":{
				"steps":[{"skills":["cigs-alert-context","soc-alert-verdict"]}]
			}
		}
	}`)
	responseBody := []byte(`{
		"messages":[{"role":"assistant","content":"IP 分析报告输出"}],
		"investigation":{
			"stages":[
				{
					"skill":"cigs-alert-context",
					"output":"确认为产品运营事件",
					"tools":[{"name":"cigs alerts history --limit 500","status":"success"}]
				}
			]
		},
		"customer_line":"这是一条用户长期未使用产品的运营提醒，不是网络攻击或设备失陷。"
	}`)

	audit := BuildUsagePromptAudit(clientRequest, nil, responseBody)

	require.NotNil(t, audit)
	assertAuditTextContains(t, audit.UserInputs, "client_request.messages[0].content", "103.195.103.66")
	assertAuditTextContains(t, audit.AssistantOutputs, "upstream_response.messages[0].content", "IP 分析报告输出")
	assertAuditTextContains(t, audit.AssistantOutputs, "upstream_response.investigation.stages[0].output", "产品运营事件")
	assertAuditTextContains(t, audit.AssistantOutputs, "upstream_response.customer_line", "长期未使用")
	assertAuditItemNamed(t, audit.Skills, "client_request.messages[0].composer.inputItems[0].metadata.capability_skills[0]", "claw-cybersec-analysis:ip-analysis")
	assertAuditItemNamed(t, audit.Skills, "client_request.profile.entry_skill", "claw-soc:soc-triage")
	assertAuditItemNamed(t, audit.Skills, "client_request.unit.dispatch.steps[0].skills[0]", "cigs-alert-context")
	assertAuditItemNamed(t, audit.ToolCalls, "upstream_response.investigation.stages[0].tools[0]", "cigs alerts history --limit 500")
	assertAuditItemNamed(t, audit.Plugins, "client_request.profile.plugin", "claw-soc")
	assertAuditItemNamed(t, audit.Plugins, "client_request.profile.required_plugins[0]", "claw-cigs")
	assertAuditItemNamed(t, audit.Plugins, "client_request.messages[0].composer.inputItems[0]", "agent-app://ip")
}

func TestBuildUsagePromptAuditFromCapturedBodiesUsesRawBodiesOnlyForExtraction(t *testing.T) {
	audit := BuildUsagePromptAuditFromCapturedBodies(UsagePromptAuditCapturedBodies{
		ClientRequestBody:          []byte(`{"input":"abc"}`),
		ClientRequestBodyTruncated: true,
	})

	require.NotNil(t, audit)
	assertAuditDoesNotStoreRawBodies(t, audit)
	assertAuditTextContains(t, audit.UserInputs, "client_request.input", "abc")
}

func TestBuildUsagePromptAuditDeduplicatesEquivalentRequestAttachments(t *testing.T) {
	clientRequest := []byte(`{
		"system":"ClawOps system prompt",
		"input":"client original question",
		"tools":[{"type":"function","name":"lookup_ip","parameters":{"type":"object"}}],
		"metadata":{"skills":["claw-cybersec-analysis:ip-analysis"],"plugins":["claw-cigs"]}
	}`)
	upstreamRequest := []byte(`{
		"system":"ClawOps system prompt",
		"input":"mapped upstream question",
		"tools":[{"type":"function","name":"lookup_ip","parameters":{"type":"object"}}],
		"metadata":{"skills":["claw-cybersec-analysis:ip-analysis"],"plugins":["claw-cigs"]}
	}`)

	audit := BuildUsagePromptAudit(clientRequest, upstreamRequest, nil)

	require.NotNil(t, audit)
	assert.Len(t, audit.UserInputs, 2)
	assert.Len(t, audit.SystemPrompts, 1)
	assert.Len(t, audit.Tools, 1)
	assert.Len(t, audit.Skills, 1)
	assert.Len(t, audit.Plugins, 1)
	assert.Equal(t, "client_request.system", audit.SystemPrompts[0].Source)
	assert.Equal(t, "client_request.tools[0]", audit.Tools[0].Source)
	assert.Equal(t, "client_request.metadata.skills[0]", audit.Skills[0].Source)
	assert.Equal(t, "client_request.metadata.plugins[0]", audit.Plugins[0].Source)
	assert.Equal(t, 1, audit.Counts["system_prompts"])
	assert.Equal(t, 1, audit.Counts["tools"])
	assert.Equal(t, 1, audit.Counts["skills"])
	assert.Equal(t, 1, audit.Counts["plugins"])
}

func TestBuildUsagePromptAuditSeparatesToolDefinitionsFromToolCalls(t *testing.T) {
	clientRequest := []byte(`{
		"tools":[{"type":"function","name":"lookup_ip"}],
		"messages":[{"role":"assistant","tool_calls":[{"id":"call_history","function":{"name":"lookup_ip","arguments":"{}"}}]}]
	}`)
	responseBody := []byte(`{
		"output":[{"type":"function_call","name":"run_triage","arguments":"{}"}]
	}`)

	audit := BuildUsagePromptAudit(clientRequest, nil, responseBody)

	require.NotNil(t, audit)
	assert.Len(t, audit.Tools, 1)
	assertAuditItemNamed(t, audit.Tools, "client_request.tools[0]", "lookup_ip")
	assert.Len(t, audit.ToolCalls, 2)
	assertAuditItemNamed(t, audit.ToolCalls, "client_request.messages[0].tool_calls[0]", "lookup_ip")
	assertAuditItemNamed(t, audit.ToolCalls, "upstream_response.output[0]", "run_triage")
	assert.Equal(t, 1, audit.Counts["tools"])
	assert.Equal(t, 2, audit.Counts["tool_calls"])
}

func TestBuildUsagePromptAuditExtractsResponsesInputToolCallsWithoutCrossBodyDuplicates(t *testing.T) {
	request := []byte(`{
		"input":[
			{"type":"function_call","call_id":"call_lookup_ip","name":"lookup_ip","arguments":"{\"ip\":\"8.8.8.8\"}"},
			{"type":"function_call_output","call_id":"call_lookup_ip","output":"no malicious result"}
		]
	}`)

	audit := BuildUsagePromptAudit(request, request, nil)

	require.NotNil(t, audit)
	require.Len(t, audit.ToolCalls, 2)
	assertAuditItemNamed(t, audit.ToolCalls, "client_request.input[0]", "lookup_ip")
	assertAuditItemNamed(t, audit.ToolCalls, "client_request.input[1]", "call_lookup_ip")
	assert.Equal(t, 2, audit.Counts["tool_calls"])
}

func TestBuildUsagePromptAuditUsesTriageSummaryAsUserVisibleOutput(t *testing.T) {
	responseBody := []byte(`{
		"output":[{
			"type":"message",
			"role":"assistant",
			"content":[{
				"type":"output_text",
				"text":"{\"determination\":\"良性\",\"headline\":\"产品运营事件\",\"summary\":\"该告警属于产品用户留存运营提醒，无需安全处置。\",\"customer_line\":\"这是一条运营提醒。\"}"
			}]
		}]
	}`)

	audit := BuildUsagePromptAudit(nil, nil, responseBody)

	require.NotNil(t, audit)
	require.Len(t, audit.AssistantOutputs, 1)
	assert.Equal(t, "该告警属于产品用户留存运营提醒，无需安全处置。", audit.AssistantOutputs[0].Text)
}

func TestBuildUsagePromptAuditPreservesInputAndOutputWhitespace(t *testing.T) {
	audit := BuildUsagePromptAudit(
		[]byte(`{"input":"\n  original user input  \n"}`),
		nil,
		[]byte(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"\n  original model output  \n"}]}]}`),
	)

	require.NotNil(t, audit)
	require.Equal(t, "\n  original user input  \n", audit.UserInputs[0].Text)
	require.Equal(t, "\n  original model output  \n", audit.AssistantOutputs[0].Text)
}

func TestNormalizeUsagePromptAuditOtherDeduplicatesLegacyAttachments(t *testing.T) {
	other := `{
		"usage_semantic":"openai",
		"audit_info":{
			"prompt_audit":{
				"schema":"newapi_prompt_audit:v1",
				"system_prompts":[
					{"source":"client_request.instructions","text":"legacy system prompt"},
					{"source":"upstream_request.instructions","text":"legacy system prompt"}
				],
				"tools":[
					{"source":"client_request.tools[0]","type":"function","name":"lookup_ip","value":{"type":"function","name":"lookup_ip"}},
					{"source":"upstream_request.tools[0]","type":"function","name":"lookup_ip","value":{"type":"function","name":"lookup_ip"}}
				],
				"counts":{"system_prompts":2,"tools":2}
			}
		}
	}`

	normalized := NormalizeUsagePromptAuditOther(other)
	var normalizedOther map[string]any
	require.NoError(t, common.Unmarshal([]byte(normalized), &normalizedOther))
	auditInfo, ok := normalizedOther["audit_info"].(map[string]any)
	require.True(t, ok)
	auditBytes, err := common.Marshal(auditInfo[UsagePromptAuditOtherKey])
	require.NoError(t, err)
	var audit UsagePromptAudit
	require.NoError(t, common.Unmarshal(auditBytes, &audit))
	assert.Len(t, audit.SystemPrompts, 1)
	assert.Len(t, audit.Tools, 1)
	assert.Equal(t, "client_request.instructions", audit.SystemPrompts[0].Source)
	assert.Equal(t, "client_request.tools[0]", audit.Tools[0].Source)
	assert.Equal(t, 1, audit.Counts["system_prompts"])
	assert.Equal(t, 1, audit.Counts["tools"])
}

func TestNormalizeUsagePromptAuditOtherMovesLegacyReplayToolsToToolCalls(t *testing.T) {
	other := `{
		"audit_info":{
			"prompt_audit":{
				"schema":"newapi_prompt_audit:v1",
				"tools":[{
					"source":"upstream_response.output[1]",
					"type":"function",
					"name":"/bin/zsh -lc 'clawops runtime status'",
					"value":{"type":"function","name":"/bin/zsh -lc 'clawops runtime status'","description":"ClawOps raw snapshot tool replay"}
				}]
			}
		}
	}`

	normalized := NormalizeUsagePromptAuditOther(other)
	var normalizedOther map[string]any
	require.NoError(t, common.Unmarshal([]byte(normalized), &normalizedOther))
	auditInfo, ok := normalizedOther["audit_info"].(map[string]any)
	require.True(t, ok)
	auditBytes, err := common.Marshal(auditInfo[UsagePromptAuditOtherKey])
	require.NoError(t, err)
	var audit UsagePromptAudit
	require.NoError(t, common.Unmarshal(auditBytes, &audit))
	assert.Empty(t, audit.Tools)
	assert.Len(t, audit.ToolCalls, 1)
	assert.Equal(t, "/bin/zsh -lc 'clawops runtime status'", audit.ToolCalls[0].Name)
	assert.Equal(t, 0, audit.Counts["tools"])
	assert.Equal(t, 1, audit.Counts["tool_calls"])
}

func TestInjectUsagePromptAuditAddsPromptAuditToOther(t *testing.T) {
	info := &relaycommon.RelayInfo{
		UsageLogRawClientRequestBody:   []byte(`{"input":"appserver 原始输入"}`),
		UsageLogRawUpstreamRequestBody: []byte(`{"input":"最终上游输入"}`),
		UsageLogRawResponseBody:        []byte(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"系统输出"}]}]}`),
	}
	other := model.NewLogOther()
	other.SetPublic("usage_semantic", "openai")

	InjectUsagePromptAudit(other, info)

	auditInfo, ok := other.Snapshot()["audit_info"].(map[string]any)
	require.True(t, ok)
	audit, ok := auditInfo[UsagePromptAuditOtherKey].(*UsagePromptAudit)
	require.True(t, ok)
	assert.Equal(t, "openai", other.Snapshot()["usage_semantic"])
	assertAuditDoesNotStoreRawBodies(t, audit)
	assertAuditTextContains(t, audit.UserInputs, "client_request.input", "appserver 原始输入")
	assertAuditTextContains(t, audit.UserInputs, "upstream_request.input", "最终上游输入")
	assertAuditTextContains(t, audit.AssistantOutputs, "upstream_response.output[0].content[0].text", "系统输出")
}

func assertAuditDoesNotStoreRawBodies(t *testing.T, audit *UsagePromptAudit) {
	t.Helper()
	data, err := common.Marshal(audit)
	require.NoError(t, err)
	jsonText := string(data)
	assert.NotContains(t, jsonText, "raw_client_request_body")
	assert.NotContains(t, jsonText, "raw_client_request_body_truncated")
	assert.NotContains(t, jsonText, "raw_upstream_request_body")
	assert.NotContains(t, jsonText, "raw_upstream_request_body_truncated")
	assert.NotContains(t, jsonText, "raw_response_body")
	assert.NotContains(t, jsonText, "raw_response_body_truncated")
}

func assertAuditTextContains(t *testing.T, texts []UsagePromptAuditText, source string, want string) {
	t.Helper()
	for _, text := range texts {
		if text.Source == source && strings.Contains(text.Text, want) {
			return
		}
	}
	require.Failf(t, "missing audit text", "source=%s want substring=%q texts=%+v", source, want, texts)
}

func assertAuditItemNamed(t *testing.T, items []UsagePromptAuditItem, source string, name string) {
	t.Helper()
	for _, item := range items {
		if item.Source == source && item.Name == name {
			return
		}
	}
	require.Failf(t, "missing audit item", "source=%s name=%s items=%+v", source, name, items)
}

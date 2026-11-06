package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetPricingEndpointTestTables(t *testing.T) {
	t.Helper()
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	require.NoError(t, DB.AutoMigrate(&Channel{}, &Ability{}, &Model{}, &Vendor{}, &Option{}))
	for _, table := range []string{"abilities", "channels", "models", "vendors"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	InitChannelCache()
	InvalidatePricingCache()
	t.Cleanup(func() {
		for _, table := range []string{"abilities", "channels", "models", "vendors"} {
			require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
		}
		InitChannelCache()
		InvalidatePricingCache()
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})
}

func insertPricingEndpointChannel(t *testing.T, channelID int, channelType int, settings dto.ChannelOtherSettings) {
	t.Helper()
	channel := &Channel{
		Id:     channelID,
		Type:   channelType,
		Key:    fmt.Sprintf("key-%d", channelID),
		Status: common.ChannelStatusEnabled,
		Name:   fmt.Sprintf("channel-%d", channelID),
	}
	if settings.AdvancedCustom != nil {
		channel.SetOtherSettings(settings)
	}
	require.NoError(t, DB.Create(channel).Error)
}

func insertPricingEndpointAbility(t *testing.T, channelID int, modelName string) {
	t.Helper()
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelID,
		Enabled:   true,
	}).Error)
}

func pricingEndpointAdvancedCustomConfig(routes ...dto.AdvancedCustomRoute) dto.ChannelOtherSettings {
	return dto.ChannelOtherSettings{
		AdvancedCustom: &dto.AdvancedCustomConfig{
			Routes: routes,
		},
	}
}

func pricingEndpointTypesByModel(t *testing.T) map[string][]constant.EndpointType {
	t.Helper()
	InitChannelCache()
	return pricingEndpointTypesFromPricing(GetPricing())
}

func pricingEndpointTypesFromPricing(pricings []Pricing) map[string][]constant.EndpointType {
	byModel := make(map[string][]constant.EndpointType)
	for _, pricing := range pricings {
		byModel[pricing.ModelName] = pricing.SupportedEndpointTypes
	}
	return byModel
}

func TestPricingAdvancedCustomUsesConfiguredEndpointTypes(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 101, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/chat/completions",
			UpstreamPath: "/v1/chat/completions",
		},
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	insertPricingEndpointAbility(t, 101, "gemini-2.5-flash")
	insertPricingEndpointAbility(t, 101, "gpt-4o")

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
	}, byModel["gemini-2.5-flash"])
	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
	}, byModel["gpt-4o"])
}

func TestUpsertModelContextMetadataFillsMissingAndPreservesConfigured(t *testing.T) {
	resetPricingEndpointTestTables(t)

	require.NoError(t, DB.Create(&Model{ModelName: "ctx-probe-auto", Status: 1, NameRule: NameRuleExact}).Error)
	require.NoError(t, DB.Create(&Model{ModelName: "ctx-probe-manual", Status: 1, NameRule: NameRuleExact, ContextLength: 123456, ContextWindow: 123456, MaxOutputTokens: 8192}).Error)

	updated, err := UpsertModelContextMetadata(map[string]ModelContextMetadata{
		"ctx-probe-auto":   {ContextLength: 393216, ContextWindow: 393216, MaxOutputTokens: 131072},
		"ctx-probe-manual": {ContextLength: 999, ContextWindow: 999, MaxOutputTokens: 16},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, updated)

	var auto Model
	require.NoError(t, DB.Where("model_name = ?", "ctx-probe-auto").First(&auto).Error)
	assert.Equal(t, 393216, auto.ContextLength)
	assert.Equal(t, 393216, auto.ContextWindow)
	assert.Equal(t, 131072, auto.MaxOutputTokens)

	var manual Model
	require.NoError(t, DB.Where("model_name = ?", "ctx-probe-manual").First(&manual).Error)
	assert.Equal(t, 123456, manual.ContextLength)
	assert.Equal(t, 123456, manual.ContextWindow)
	assert.Equal(t, 8192, manual.MaxOutputTokens)
}

func TestUpsertModelContextMetadataKeepsOutputOnlyMetadata(t *testing.T) {
	resetPricingEndpointTestTables(t)

	updated, err := UpsertModelContextMetadata(map[string]ModelContextMetadata{
		"output-only-model": {MaxOutputTokens: 131072},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, updated)

	metadata := GetModelContextMetadataForModels([]string{"output-only-model"})
	require.Equal(t, ModelContextMetadata{MaxOutputTokens: 131072}, metadata["output-only-model"])
}

func TestUpsertUpstreamModelContextMetadataRefreshesStaleAndSharesConservativeLimits(t *testing.T) {
	resetPricingEndpointTestTables(t)

	require.NoError(t, DB.Create(&Model{
		ModelName:       "shared-upstream-model",
		ContextLength:   1_000_000,
		ContextWindow:   1_000_000,
		MaxOutputTokens: 1_000_000,
		Status:          1,
		NameRule:        NameRuleExact,
	}).Error)

	updated, err := UpsertUpstreamModelContextMetadata(map[string]ModelContextMetadata{
		"shared-upstream-model": {
			ContextLength:   393216,
			ContextWindow:   393216,
			MaxOutputTokens: 131072,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, updated)

	var refreshed Model
	require.NoError(t, DB.Where("model_name = ?", "shared-upstream-model").First(&refreshed).Error)
	assert.Equal(t, 393216, refreshed.ContextLength)
	assert.Equal(t, 393216, refreshed.ContextWindow)
	assert.Equal(t, 131072, refreshed.MaxOutputTokens)

	updated, err = UpsertUpstreamModelContextMetadata(map[string]ModelContextMetadata{
		"shared-upstream-model": {
			ContextLength:   500000,
			ContextWindow:   500000,
			MaxOutputTokens: 200000,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, updated)

	require.NoError(t, DB.Where("model_name = ?", "shared-upstream-model").First(&refreshed).Error)
	assert.Equal(t, 393216, refreshed.ContextLength)
	assert.Equal(t, 393216, refreshed.ContextWindow)
	assert.Equal(t, 131072, refreshed.MaxOutputTokens)
}

func TestParseContextProbeErrors(t *testing.T) {
	contextLength, err := parseContextLimitFromError([]byte(`{"error":"maximum context length is 393216 tokens"}`), 400)
	require.NoError(t, err)
	assert.Equal(t, 393216, contextLength)

	maxOutput, err := parseMaxOutputTokensFromError([]byte(`{"error":"max_output_tokens must be <= 131072"}`), 400)
	require.NoError(t, err)
	assert.Equal(t, 131072, maxOutput)

	accepted, err := parseContextProbeResponse([]byte(`{"usage":{"prompt_tokens":200000}}`), 200)
	require.NoError(t, err)
	assert.Zero(t, accepted)

	_, err = parseContextLimitFromError([]byte(`{"error":"prompt is too long: 200000 tokens"}`), 400)
	require.Error(t, err)
}

func TestProbeModelMaxOutputTokensDoesNotInferFromSuccessfulUsage(t *testing.T) {
	originalPoster := ContextProbePoster
	t.Cleanup(func() { ContextProbePoster = originalPoster })
	ContextProbePoster = func(_ *Channel, _ []byte) ([]byte, int, error) {
		return []byte(`{"usage":{"completion_tokens":1}}`), 200, nil
	}

	limit, err := probeModelMaxOutputTokens(&Channel{}, "output-probe-model")
	require.NoError(t, err)
	assert.Zero(t, limit)
}

func TestDetectModelContextMetadataDoesNotProbeOutputByDefault(t *testing.T) {
	t.Setenv("MAX_OUTPUT_TOKENS_PROBE_ENABLED", "false")
	originalPoster := ContextProbePoster
	t.Cleanup(func() { ContextProbePoster = originalPoster })
	called := false
	ContextProbePoster = func(_ *Channel, _ []byte) ([]byte, int, error) {
		called = true
		return nil, 0, nil
	}

	metadata, err := detectModelContextMetadata(&Channel{}, "known-context-model", ModelContextMetadata{
		ContextLength: 393216,
		ContextWindow: 393216,
	})
	require.NoError(t, err)
	assert.Equal(t, 393216, metadata.ContextLength)
	assert.False(t, called)
}

func TestContextProbeIsRateLimitedPerChannelAndModel(t *testing.T) {
	channel := &Channel{Id: 987654, Type: 1}
	modelName := "context-probe-rate-limit"
	contextProbeAttempts.Delete(contextProbeKey(channel, modelName))

	assert.True(t, claimContextProbe(channel, modelName))
	assert.False(t, claimContextProbe(channel, modelName))
}

func TestPricingExposesModelContextMetadata(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 104, constant.ChannelTypeOpenAI, dto.ChannelOtherSettings{})
	insertPricingEndpointAbility(t, 104, "ctx-probe-listed")
	require.NoError(t, DB.Create(&Model{
		ModelName:       "ctx-probe-listed",
		ContextLength:   393216,
		ContextWindow:   393216,
		MaxOutputTokens: 131072,
		Status:          1,
		NameRule:        NameRuleExact,
	}).Error)

	byModel := make(map[string]Pricing)
	for _, pricing := range GetPricing() {
		byModel[pricing.ModelName] = pricing
	}

	pricing, ok := byModel["ctx-probe-listed"]
	require.True(t, ok)
	assert.Equal(t, 393216, pricing.ContextLength)
	assert.Equal(t, 393216, pricing.ContextWindow)
	assert.Equal(t, 131072, pricing.MaxOutputTokens)
	assert.Equal(t, 393216, GetModelContextLength("ctx-probe-listed"))
	assert.Equal(t, 393216, GetModelContextWindow("ctx-probe-listed"))
	assert.Equal(t, 131072, GetModelMaxOutputTokens("ctx-probe-listed"))
}

func TestPricingModelMetadataEndpointsMergeWithAdvancedCustomInference(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 103, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	insertPricingEndpointAbility(t, 103, "gemini-2.5-flash")
	require.NoError(t, DB.Create(&Model{
		ModelName: "gemini-2.5-flash",
		Endpoints: `{
			"openai": "/v1/chat/completions"
		}`,
		Status:   1,
		NameRule: NameRuleExact,
	}).Error)

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAIResponse,
		constant.EndpointTypeOpenAI,
	}, byModel["gemini-2.5-flash"])
}

func TestPricingModelMetadataEndpointsCanProvideEndpointWithoutChannelInference(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 104, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	insertPricingEndpointAbility(t, 104, "metadata-only-model")
	require.NoError(t, DB.Create(&Model{
		ModelName: "metadata-only-model",
		Endpoints: `{
			"openai": "/v1/chat/completions"
		}`,
		Status:   1,
		NameRule: NameRuleExact,
	}).Error)

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, byModel["metadata-only-model"])
}

func TestPricingAdvancedCustomMissingConfigFallsBackToChannelType(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 102, constant.ChannelTypeAdvancedCustom, dto.ChannelOtherSettings{})
	insertPricingEndpointAbility(t, 102, "gpt-4o")

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, byModel["gpt-4o"])
}

func TestPricingNativeChannelEndpointTypesUnchanged(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 201, constant.ChannelTypeOpenAI, dto.ChannelOtherSettings{})
	insertPricingEndpointChannel(t, 202, constant.ChannelTypeGemini, dto.ChannelOtherSettings{})
	insertPricingEndpointChannel(t, 203, constant.ChannelTypeAnthropic, dto.ChannelOtherSettings{})
	insertPricingEndpointAbility(t, 201, "gpt-4o")
	insertPricingEndpointAbility(t, 202, "gemini-2.5-flash")
	insertPricingEndpointAbility(t, 203, "claude-3-5-sonnet")

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, byModel["gpt-4o"])
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeGemini, constant.EndpointTypeOpenAI}, byModel["gemini-2.5-flash"])
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI}, byModel["claude-3-5-sonnet"])
}

func TestInitChannelCacheInvalidatesPricingCache(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 301, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/chat/completions",
			UpstreamPath: "/v1/chat/completions",
		},
	))
	insertPricingEndpointAbility(t, 301, "gemini-3.5-flash")
	InitChannelCache()

	initial := pricingEndpointTypesByModel(t)
	require.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, initial["gemini-3.5-flash"])

	var channel Channel
	require.NoError(t, DB.First(&channel, "id = ?", 301).Error)
	channel.SetOtherSettings(pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/chat/completions",
			UpstreamPath: "/v1/chat/completions",
		},
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 301).Update("settings", channel.OtherSettings).Error)
	InitChannelCache()

	updated := pricingEndpointTypesByModel(t)
	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
	}, updated["gemini-3.5-flash"])
}

func TestInitChannelCacheInvalidatesStartupPricingBuiltBeforeChannelCache(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 302, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/chat/completions",
			UpstreamPath: "/v1/chat/completions",
		},
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	insertPricingEndpointAbility(t, 302, "gemini-3.5-flash")

	staleByModel := pricingEndpointTypesFromPricing(GetPricing())
	require.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, staleByModel["gemini-3.5-flash"])

	InitChannelCache()

	rebuiltByModel := pricingEndpointTypesFromPricing(GetPricing())
	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
	}, rebuiltByModel["gemini-3.5-flash"])
}

func TestCacheUpdateChannelSyncsAdvancedCustomConfig(t *testing.T) {
	resetPricingEndpointTestTables(t)

	channel := &Channel{
		Id:     401,
		Type:   constant.ChannelTypeAdvancedCustom,
		Key:    "key-401",
		Status: common.ChannelStatusEnabled,
		Name:   "channel-401",
	}
	channel.SetOtherSettings(pricingEndpointAdvancedCustomConfig(dto.AdvancedCustomRoute{
		IncomingPath: "/v1/responses",
		UpstreamPath: "/v1beta/models/{model}:generateContent",
		Converter:    "openai_responses_to_gemini_generate_content",
	}))
	CacheUpdateChannel(channel)

	require.NotNil(t, channel2advancedCustomConfig[401])
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAIResponse}, channel2advancedCustomConfig[401].SupportedEndpointTypesForModel("gemini-3.5-flash"))

	channel.SetOtherSettings(pricingEndpointAdvancedCustomConfig(dto.AdvancedCustomRoute{
		IncomingPath: "/v1/chat/completions",
		UpstreamPath: "/v1/chat/completions",
	}))
	CacheUpdateChannel(channel)

	require.NotNil(t, channel2advancedCustomConfig[401])
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, channel2advancedCustomConfig[401].SupportedEndpointTypesForModel("gemini-3.5-flash"))

	channel.Type = constant.ChannelTypeOpenAI
	CacheUpdateChannel(channel)

	assert.Nil(t, channel2advancedCustomConfig[401])
}

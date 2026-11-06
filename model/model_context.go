package model

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	contextLengthProbePromptChars = 256 * 1024
)

// ModelContextMetadata carries the model limits exposed through /v1/models.
type ModelContextMetadata struct {
	ContextLength   int `json:"context_length,omitempty"`
	ContextWindow   int `json:"context_window,omitempty"`
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`
}

// ContextProbePoster is injected by the transport layer to avoid an import
// cycle between model and controller packages.
var ContextProbePoster func(channel *Channel, payload []byte) ([]byte, int, error)

// UpsertModelContextMetadata persists discovered context limits without
// creating metadata rows or overwriting an administrator-configured value.
func UpsertModelContextMetadata(metadata map[string]ModelContextMetadata) (int, error) {
	if len(metadata) == 0 {
		return 0, nil
	}
	result := 0
	err := metadataTransaction(func(tx *gorm.DB) error {
		for modelName, values := range metadata {
			modelName = strings.TrimSpace(modelName)
			if modelName == "" {
				continue
			}
			if values.ContextLength <= 0 {
				values.ContextLength = values.ContextWindow
			}
			if values.ContextWindow <= 0 {
				values.ContextWindow = values.ContextLength
			}
			var existing Model
			err := tx.Where("model_name = ?", modelName).First(&existing).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if values.ContextLength <= 0 {
					continue
				}
				now := common.GetTimestamp()
				if err := tx.Create(&Model{
					ModelName:       modelName,
					ContextLength:   values.ContextLength,
					ContextWindow:   values.ContextWindow,
					MaxOutputTokens: values.MaxOutputTokens,
					Status:          1,
					SyncOfficial:    1,
					NameRule:        NameRuleExact,
					CreatedTime:     now,
					UpdatedTime:     now,
				}).Error; err != nil {
					return err
				}
				result++
				continue
			}
			if err != nil {
				return err
			}
			updates := map[string]any{"updated_time": common.GetTimestamp()}
			if existing.ContextLength <= 0 && values.ContextLength > 0 {
				updates["context_length"] = values.ContextLength
			}
			if existing.ContextWindow <= 0 && values.ContextWindow > 0 {
				updates["context_window"] = values.ContextWindow
			}
			if existing.MaxOutputTokens <= 0 && values.MaxOutputTokens > 0 {
				updates["max_output_tokens"] = values.MaxOutputTokens
			}
			if len(updates) > 1 {
				if err := tx.Model(&Model{}).Where("id = ?", existing.Id).Updates(updates).Error; err != nil {
					return err
				}
				result++
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return result, nil
}

// GetModelContextMetadataForModels returns the context metadata currently
// stored in the metadata table for the supplied concrete model names.
func GetModelContextMetadataForModels(modelNames []string) map[string]ModelContextMetadata {
	result := make(map[string]ModelContextMetadata, len(modelNames))
	if len(modelNames) == 0 {
		return result
	}
	var records []Model
	if err := DB.Select("model_name", "context_length", "context_window", "max_output_tokens").Find(&records).Error; err != nil {
		return result
	}
	resolved := resolveModelMetadata(records, modelNames)
	for _, modelName := range modelNames {
		if meta := resolved[modelName]; meta != nil && meta.ContextLength > 0 {
			result[modelName] = ModelContextMetadata{
				ContextLength:   meta.ContextLength,
				ContextWindow:   meta.ContextWindow,
				MaxOutputTokens: meta.MaxOutputTokens,
			}
		}
	}
	return result
}

// DetectModelContextMetadata probes models whose limits are still unknown.
// A successful oversized request caps the unknown window; a context-limit
// error is parsed from the response body or HTTP status.
func DetectModelContextMetadata(channel *Channel, modelNames []string) (map[string]ModelContextMetadata, error) {
	if len(modelNames) == 0 {
		return map[string]ModelContextMetadata{}, nil
	}
	known := make(map[string]bool)
	var records []Model
	if err := DB.Select("model_name", "context_length", "context_window", "max_output_tokens").Find(&records).Error; err != nil {
		return nil, err
	}
	resolved := resolveModelMetadata(records, modelNames)
	discovered := make(map[string]ModelContextMetadata)
	for _, modelName := range modelNames {
		if meta := resolved[modelName]; meta != nil {
			values := ModelContextMetadata{
				ContextLength:   meta.ContextLength,
				ContextWindow:   meta.ContextWindow,
				MaxOutputTokens: meta.MaxOutputTokens,
			}
			if values.ContextWindow <= 0 {
				values.ContextWindow = values.ContextLength
			}
			discovered[modelName] = values
			if values.ContextLength > 0 && values.MaxOutputTokens > 0 {
				known[modelName] = true
			}
		}
	}

	for _, modelName := range modelNames {
		if known[modelName] {
			continue
		}
		metadata, err := detectModelContextMetadata(channel, modelName, discovered[modelName])
		if err != nil {
			return discovered, fmt.Errorf("model %s: %w", modelName, err)
		}
		if metadata.ContextLength > 0 || metadata.ContextWindow > 0 || metadata.MaxOutputTokens > 0 {
			discovered[modelName] = metadata
		}
	}
	return discovered, nil
}

func detectModelContextMetadata(channel *Channel, modelName string, existing ModelContextMetadata) (ModelContextMetadata, error) {
	metadata := existing
	if metadata.ContextLength <= 0 {
		length, err := probeModelContextLength(channel, modelName, contextLengthProbePromptChars)
		if err != nil {
			return metadata, err
		}
		if length > 0 {
			metadata.ContextLength = length
		}
	}
	if metadata.ContextWindow <= 0 {
		metadata.ContextWindow = metadata.ContextLength
	}
	if metadata.MaxOutputTokens <= 0 {
		outputLimit, err := probeModelMaxOutputTokens(channel, modelName)
		if err != nil {
			return metadata, nil
		}
		if outputLimit > 0 {
			metadata.MaxOutputTokens = outputLimit
		}
	}
	return metadata, nil
}

func probeModelMaxOutputTokens(channel *Channel, modelName string) (int, error) {
	if ContextProbePoster == nil {
		return 0, errors.New("context probe transport is unavailable")
	}
	payload := buildMaxOutputProbePayload(modelName)
	body, status, err := ContextProbePoster(channel, payload)
	if err != nil {
		return 0, err
	}
	if status == http.StatusOK {
		var result struct {
			Usage struct {
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := common.Unmarshal(body, &result); err == nil && result.Usage.CompletionTokens > 0 {
			return result.Usage.CompletionTokens, nil
		}
		return 0, nil
	}
	return parseMaxOutputTokensFromError(body, status)
}

func buildMaxOutputProbePayload(modelName string) []byte {
	request := map[string]any{
		"model":      modelName,
		"messages":   []map[string]any{{"role": "user", "content": "hi"}},
		"max_tokens": 1 << 30,
	}
	data, _ := common.Marshal(request)
	return data
}

var maxOutputTokensPattern = regexp.MustCompile(`(?i)(?:max(?:imum)?(?:_|\s+)?(?:output|completion)(?:_|\s+)?tokens?)[^\d]{0,80}(\d[\d,_.]*)`)

func parseMaxOutputTokensFromError(body []byte, statusCode int) (int, error) {
	text := strings.ToLower(string(body))
	matches := maxOutputTokensPattern.FindAllStringSubmatch(text, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		value, parseErr := strconv.Atoi(strings.NewReplacer(",", "", "_", "", ".", "").Replace(match[1]))
		if parseErr == nil && value > 0 && value <= 1<<31 {
			return value, nil
		}
	}
	return 0, nil
}

func probeModelContextLength(channel *Channel, modelName string, promptChars int) (int, error) {
	if ContextProbePoster == nil {
		return 0, errors.New("context probe transport is unavailable")
	}
	payload := buildContextProbePayload(modelName, promptChars)
	body, status, err := ContextProbePoster(channel, payload)
	if err != nil {
		return 0, err
	}
	return parseContextProbeResponse(body, status)
}

func buildContextProbePayload(modelName string, promptChars int) []byte {
	// Keep the probe on the smallest common request shape and avoid billing
	// completion work: a short output cap is enough to elicit a limit error.
	request := map[string]any{
		"model":      modelName,
		"messages":   []map[string]any{{"role": "user", "content": repeatContextProbeToken("x", promptChars)}},
		"max_tokens": 1,
	}
	data, _ := common.Marshal(request)
	return data
}

func repeatContextProbeToken(token string, count int) string {
	if count <= 0 {
		return token
	}
	var builder strings.Builder
	builder.Grow(len(token) * count)
	for range count {
		builder.WriteString(token)
	}
	return builder.String()
}

// PostJSONForContextProbe issues the context-discovery request through the
// channel proxy configuration supplied by the caller.
func PostJSONForContextProbe(url string, channel *Channel, headers http.Header, payload []byte, client *http.Client) ([]byte, int, error) {
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
		if strings.EqualFold(name, "Host") {
			request.Host = headers.Get(name)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil {
		return nil, response.StatusCode, readErr
	}
	return body, response.StatusCode, nil
}

func parseContextProbeResponse(body []byte, statusCode int) (int, error) {
	if statusCode == http.StatusOK {
		return 0, nil
	}
	return parseContextLimitFromError(body, statusCode)
}

var contextLimitNumberPattern = regexp.MustCompile(`(?i)(?:maximum(?:_|\s+)?context(?:_|\s+)?length|context(?:_|\s+)?length(?:_|\s+)?(?:is|exceeds|limit)|max(?:imum)?(?:_|\s+)?(?:context|input|prompt)(?:_|\s+)?(?:length|tokens?)|prompt(?:_|\s+)?(?:is\s+)?too\s+long)[^\d]{0,80}(\d[\d,_.]*)`)

func parseContextLimitFromError(body []byte, statusCode int) (int, error) {
	text := strings.ToLower(string(body))
	matches := contextLimitNumberPattern.FindAllStringSubmatch(text, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		value, parseErr := strconv.Atoi(strings.NewReplacer(",", "", "_", "", ".", "").Replace(match[1]))
		if parseErr == nil && value > 0 && value <= 1<<31 {
			return value, nil
		}
	}
	if statusCode == http.StatusRequestEntityTooLarge {
		// No explicit limit was reported; retry with a smaller payload.
		return 0, nil
	}
	return 0, fmt.Errorf("upstream did not report a context limit (status %d)", statusCode)
}

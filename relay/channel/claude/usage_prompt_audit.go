package claude

import (
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
)

func recordUsagePromptAuditResponseBody(info *relaycommon.RelayInfo, body []byte) {
	if info == nil || len(body) == 0 {
		return
	}
	info.UsageLogRawResponseBody, info.UsageLogRawResponseBodyTruncated = service.CaptureUsagePromptAuditBytes(body)
}

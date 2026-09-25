package agentjob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"easygpa/backend/internal/agentcontext"
	"easygpa/backend/internal/agenttools"
	"easygpa/backend/internal/knowledge"
	"easygpa/backend/internal/llm"
)

// Converter OCR shares the message's frozen vision route. A route change while
// the job is queued must not silently send the same evidence to another vendor.
type evidenceVisionClient struct {
	client llm.Client
	route  *llm.Route
}

func (c evidenceVisionClient) Call(ctx context.Context, request llm.Request) (llm.Response, error) {
	request.Purpose = llm.PurposeAgentVision
	request.Route = c.route
	return c.client.Call(ctx, request)
}

func (w *Worker) evidenceReader(runtime Runtime, route *llm.Route) agenttools.EvidenceReader {
	return func(ctx context.Context, file agentcontext.Evidence) (agenttools.EvidenceContent, error) {
		var result agenttools.EvidenceContent
		reader, err := w.objects.Open(ctx, file.ObjectKey)
		if err != nil {
			return result, err
		}
		data, err := io.ReadAll(io.LimitReader(reader, file.SizeBytes+1))
		reader.Close()
		if err != nil || int64(len(data)) != file.SizeBytes {
			return result, errors.New("evidence size mismatch")
		}
		sum := sha256.Sum256(data)
		if file.SHA256 != "" && !strings.EqualFold(hex.EncodeToString(sum[:]), file.SHA256) {
			return result, errors.New("evidence checksum mismatch")
		}
		dir, err := os.MkdirTemp("", "easygpa-agent-evidence-")
		if err != nil {
			return result, err
		}
		defer os.RemoveAll(dir)
		local := filepath.Join(dir, "source")
		if err = os.WriteFile(local, data, 0600); err != nil {
			return result, err
		}
		media, warning, err := knowledge.DetectMediaType(local, file.Filename, file.MediaType)
		if err != nil {
			return result, err
		}
		if media == "image/png" || media == "image/jpeg" || media == "image/webp" || media == "image/gif" {
			result.Images = []llm.Image{{MediaType: media, Data: data}}
			result.Warning = warning
			return result, nil
		}
		// Archives are not expanded as business evidence. They remain available
		// for human download and are never mistaken for inspected originals.
		if strings.EqualFold(filepath.Ext(file.Filename), ".zip") || media == "application/zip" {
			result.Warning = "压缩包未展开，请人工查看原件"
			return result, nil
		}
		converter := knowledge.Converter{Vision: evidenceVisionClient{w.client, route}, AllowOCR: route != nil,
			CommandTime: 30 * time.Second, MaxPDFPages: 8, MaxExtractedBytes: 2 << 20, DisableNativeTools: !runtime.Flags.NativeToolsEnabled}
		converted, err := converter.Convert(ctx, local, file.Filename, media)
		if err != nil {
			return result, err
		}
		result.Warning = converted.Warning
		result.Usage = converted.Usage
		var text strings.Builder
		for _, entry := range converted.Entries {
			if entry.Kind == "metadata" {
				continue
			}
			part := entry.Text
			if page, ok := entry.Locator["page"]; ok {
				part = "\n页码：" + fmt.Sprint(page) + "\n" + part
			}
			part += "\n"
			if remaining := 12000 - text.Len(); len(part) > remaining {
				part = part[:remaining]
				for !utf8.ValidString(part) {
					part = part[:len(part)-1]
				}
				text.WriteString(part)
				result.Warning += "；本轮只读取原文前 12000 字节，后文未读"
				break
			}
			text.WriteString(part)
		}
		result.Text = text.String()
		if result.Text == "" && result.Warning == "" {
			result.Warning = "未提取到可读正文，请人工核对原件"
		}
		return result, nil
	}
}

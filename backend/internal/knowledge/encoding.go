package knowledge

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// GB18030 decoders replace malformed input without necessarily returning an
// error. Require a lossless round trip before treating unknown bytes as text.
func decodeGB18030(raw []byte) ([]byte, error) {
	decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(raw)
	if err != nil || !utf8.Valid(decoded) {
		return nil, errors.New("文本既不是 UTF-8 也不是可识别的 GB18030")
	}
	roundTrip, err := simplifiedchinese.GB18030.NewEncoder().Bytes(decoded)
	if err != nil || !bytes.Equal(roundTrip, raw) {
		return nil, errors.New("文本包含无法无损解码的字节")
	}
	return decoded, nil
}

func decodeArchiveName(member *zip.File) (string, error) {
	if utf8.ValidString(member.Name) {
		return member.Name, nil
	}
	if member.Flags&(1<<11) != 0 {
		return "", errors.New("压缩包成员声明为 UTF-8，但文件名编码无效")
	}
	decoded, err := decodeGB18030([]byte(member.Name))
	if err != nil {
		return "", errors.New("压缩包文件名既不是 UTF-8 也不是可识别的 GB18030")
	}
	return string(decoded), nil
}

func decodeCommandOutput(raw []byte, maximum int64) ([]byte, error) {
	text, _, err := decodeText(raw)
	if err != nil {
		return nil, fmt.Errorf("转换工具输出编码无效: %w", err)
	}
	if int64(len(text)) > maximum {
		return nil, errors.New("转换工具输出转为 UTF-8 后超过文本大小限制")
	}
	return []byte(text), nil
}

// ValidateText is also called at the persistence boundary, before any derived
// object is written. PostgreSQL text/JSONB cannot contain invalid UTF-8 or NUL.
func (r Result) ValidateText() error {
	if !validText(r.Status) || !validText(r.Extractor) || !validText(r.Warning) || !validTextValue(r.Meta) {
		return errors.New("转换结果元数据包含无效 UTF-8 或空字符")
	}
	for index, entry := range r.Entries {
		if !validText(entry.Text) || !validText(entry.Kind) || !validTextValue(entry.Locator) || !validTextValue(entry.Metadata) {
			return fmt.Errorf("转换结果第 %d 项包含无效 UTF-8 或空字符", index+1)
		}
	}
	return nil
}

func validText(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func validTextValue(value any) bool {
	switch value := value.(type) {
	case string:
		return validText(value)
	case map[string]any:
		for key, item := range value {
			if !validText(key) || !validTextValue(item) {
				return false
			}
		}
	case map[string]string:
		for key, item := range value {
			if !validText(key) || !validText(item) {
				return false
			}
		}
	case []any:
		for _, item := range value {
			if !validTextValue(item) {
				return false
			}
		}
	case []string:
		for _, item := range value {
			if !validText(item) {
				return false
			}
		}
	}
	return true
}

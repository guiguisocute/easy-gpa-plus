package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

type brandingConfig struct {
	SVG      string `json:"svg"`
	Revision string `json:"revision"`
}

// Passive shapes and bounded embedded PNG/JPEG are accepted. SVG is served as
// JSON and rendered through an image, never inserted as document markup.
func validateCrestSVG(svg string) error {
	if len(svg) > 128*1024 {
		return errors.New("SVG 不能超过 128 KB")
	}
	if svg == "" {
		return nil
	}
	elements := strings.Fields("svg g path rect circle ellipse line polyline polygon defs linearGradient radialGradient stop clipPath title desc image")
	attrs := strings.Fields("viewBox width height x y x1 y1 x2 y2 cx cy r rx ry d points fill fill-rule fill-opacity stroke stroke-width stroke-linecap stroke-linejoin stroke-miterlimit stroke-dasharray stroke-dashoffset stroke-opacity opacity transform id offset stop-color stop-opacity gradientUnits gradientTransform spreadMethod clip-path clip-rule preserveAspectRatio version")
	allowed := func(values []string, key string) bool {
		for _, v := range values {
			if v == key {
				return true
			}
		}
		return false
	}
	decoder := xml.NewDecoder(strings.NewReader(svg))
	depth, nodes, roots := 0, 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("SVG 格式不正确")
		}
		switch t := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if t.Name.Local != "svg" {
					return errors.New("文件必须以 svg 元素开始")
				}
			}
			depth++
			nodes++
			if depth > 32 || nodes > 5000 || (t.Name.Space != "" && t.Name.Space != "http://www.w3.org/2000/svg") || !allowed(elements, t.Name.Local) {
				return errors.New("SVG 支持矢量形状与内嵌 PNG/JPEG，不支持脚本、文字、样式表或引用元素")
			}
			for _, a := range t.Attr {
				if a.Name.Local == "xmlns" || a.Name.Space == "xmlns" {
					continue
				}
				if t.Name.Local == "image" && a.Name.Local == "href" && (a.Name.Space == "" || a.Name.Space == "http://www.w3.org/1999/xlink") {
					if err := validateCrestRaster(a.Value); err != nil {
						return err
					}
					continue
				}
				if a.Name.Space != "" || !allowed(attrs, a.Name.Local) {
					return fmt.Errorf("SVG 不支持属性 %s，请将样式转换为形状属性", a.Name.Local)
				}
				v := strings.TrimSpace(a.Value)
				lower := strings.ToLower(v)
				if strings.ContainsAny(v, "\\<>") || strings.Contains(lower, "javascript:") || strings.Contains(lower, "data:") || strings.Contains(lower, "http:") || strings.Contains(lower, "https:") {
					return errors.New("SVG 不能引用外部资源")
				}
				if strings.Contains(lower, "url") && !(strings.HasPrefix(v, "url(#") && strings.HasSuffix(v, ")") && !strings.ContainsAny(v[5:len(v)-1], " ()\"'")) {
					return errors.New("SVG 只允许本地渐变或裁切引用")
				}
			}
		case xml.EndElement:
			depth--
		case xml.Directive:
			return errors.New("SVG 不允许文档类型或实体声明")
		case xml.ProcInst:
			if t.Target != "xml" || roots > 0 {
				return errors.New("SVG 不允许处理指令")
			}
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(t)) != "" {
				return errors.New("SVG 内容不正确")
			}
		}
	}
	if roots != 1 || depth != 0 {
		return errors.New("请选择有效的 SVG 文件")
	}
	return nil
}

func validateCrestRaster(value string) error {
	format := ""
	for _, candidate := range []string{"png", "jpeg"} {
		prefix := "data:image/" + candidate + ";base64,"
		if strings.HasPrefix(value, prefix) {
			format = candidate
			value = strings.TrimPrefix(value, prefix)
			break
		}
	}
	if format == "" {
		return errors.New("SVG 图片仅支持内嵌 PNG/JPEG，不允许外部地址或嵌套 SVG")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil {
		return errors.New("SVG 内嵌图片编码不正确")
	}
	cfg, actual, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || actual != format || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 4096 || cfg.Height > 4096 || int64(cfg.Width)*int64(cfg.Height) > 4_000_000 {
		return errors.New("SVG 内嵌图片须为有效 PNG/JPEG，边长不超过 4096 且总像素不超过 400 万")
	}
	return nil
}

func (s *Server) publicBranding(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	var value brandingConfig
	var raw []byte
	err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `SELECT value FROM ops_config WHERE key='branding'`).Scan(&raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeServiceError(c, err)
		return
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &value); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	c.Header("Cache-Control", "no-cache")
	c.JSON(http.StatusOK, value)
}

func (s *Server) updateBranding(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	var value brandingConfig
	if c.ShouldBindJSON(&value) != nil {
		writeError(c, 400, "invalid_branding", "请选择 SVG 文件", nil)
		return
	}
	value.SVG = strings.TrimSpace(value.SVG)
	if err := validateCrestSVG(value.SVG); err != nil {
		writeError(c, 400, "invalid_svg", err.Error(), nil)
		return
	}
	value.Revision = fmt.Sprintf("%x", sha256.Sum256([]byte(value.SVG)))
	raw, _ := json.Marshal(value)
	tx, err := s.deps.Pools.Ops.Begin(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer tx.Rollback(c.Request.Context())
	if _, err = tx.Exec(c.Request.Context(), `INSERT INTO ops_config(key,value) VALUES('branding',$1) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`, raw); err != nil {
		writeServiceError(c, err)
		return
	}
	// Store only a content digest in the audit, never the uploaded source.
	audit, _ := json.Marshal(gin.H{"revision": value.Revision, "hasCrest": value.SVG != ""})
	if _, err = tx.Exec(c.Request.Context(), `INSERT INTO ops_audit(actor,action,resource_type,metadata,ip_address) VALUES($1,'branding.update','branding',$2,NULLIF($3,'')::inet)`, s.cfg.OpsAccount, audit, c.ClientIP()); err != nil {
		writeServiceError(c, err)
		return
	}
	if err = tx.Commit(c.Request.Context()); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, value)
}

package api

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// Explicitly select non-secret startup settings. Never serialize Config itself:
// it also contains database URLs, authentication secrets and provider keys.
func (s *Server) deploymentConfiguration() gin.H {
	cfg := s.cfg
	proxies := append([]string{}, cfg.TrustedProxies...)
	return gin.H{
		"publicUrl":      cfg.PublicURL,
		"trustedProxies": proxies,
		"auth": gin.H{
			"cookieSecure":           cfg.CookieSecure,
			"accessTokenTtlSeconds":  int64(cfg.AccessTokenTTL.Seconds()),
			"refreshTokenTtlSeconds": int64(cfg.RefreshTokenTTL.Seconds()),
		},
		"mcp": gin.H{"enabled": !cfg.MCPDisabled, "maxTtlHours": cfg.MCPMaxTTLHours},
		"storage": gin.H{
			"endpoint": cfg.S3Endpoint, "publicEndpoint": cfg.S3PublicEndpoint,
			"bucket": cfg.S3Bucket, "region": cfg.S3Region,
			"useSsl": cfg.S3UseSSL, "publicUseSsl": cfg.S3PublicUseSSL,
		},
		"backup": gin.H{
			"directory": cfg.BackupDir, "offsiteDirectory": cfg.BackupOffsiteDir,
			"remoteRecipientReady": strings.TrimSpace(cfg.BackupRemoteRecipient) != "",
		},
		"secrets": gin.H{
			"mailReady": s.deps.MailCipher != nil, "aiReady": s.deps.AICipher != nil,
			"backupRemoteReady": s.deps.BackupRemoteCipher != nil,
		},
	}
}

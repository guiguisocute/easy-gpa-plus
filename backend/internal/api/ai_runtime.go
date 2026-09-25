package api

import (
	"context"
	"errors"
	"time"

	"easygpa/backend/internal/opsconfig"
)

func (s *Server) aiFallback() opsconfig.AI {
	fallback := opsconfig.DefaultAI()
	fallback.BaseURL = s.cfg.LLMBaseURL
	fallback.APIKey = s.cfg.LLMAPIKey
	fallback.TextModel = s.cfg.LLMTextModel
	fallback.VisionModel = s.cfg.LLMVisionModel
	fallback.AgentModel = s.cfg.LLMAgentModel
	fallback.MaterialMaxItems = s.cfg.AIMaxBatch
	fallback.MaterialMaxPDFPages = s.cfg.AIMaxPDFPages
	fallback.MaterialConcurrency = s.cfg.AIConcurrency
	return fallback
}

func (s *Server) runtimeModelBinding(ctx context.Context, purpose opsconfig.ModelPurpose) (opsconfig.ModelBinding, error) {
	if routes, ok := s.opsConfig.(opsconfig.ModelRouteSource); ok {
		provider, route, err := routes.ModelRoute(ctx, purpose)
		if err == nil {
			return opsconfig.ResolveModelBinding(provider, route, s.deps.AICipher, s.cfg.AllowPrivateAINetwork())
		}
		if !errors.Is(err, opsconfig.ErrModelRouteNotFound) {
			return opsconfig.ModelBinding{}, err
		}
	}
	runtime, err := s.runtimeAI(ctx)
	if err != nil {
		return opsconfig.ModelBinding{}, err
	}
	timeout := s.cfg.LLMTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return opsconfig.LegacyModelBinding(runtime, purpose, timeout)
}

func (s *Server) runtimeModelBindingsReady(ctx context.Context, purposes ...opsconfig.ModelPurpose) error {
	for _, purpose := range purposes {
		if _, err := s.runtimeModelBinding(ctx, purpose); err != nil {
			return err
		}
	}
	return nil
}

// runtimeAISettings returns operational budgets and limits without requiring
// the deprecated single-provider connection to be complete. Model credentials
// are resolved independently by runtimeModelBinding.
func (s *Server) runtimeAISettings(ctx context.Context) (opsconfig.AI, error) {
	if s.opsConfig != nil {
		return s.opsConfig.AI(ctx)
	}
	return s.aiFallback(), nil
}

// With an operational database the web switch is authoritative. AI_ENABLED is
// retained only as a fallback for stripped-down deployments without that store.
func (s *Server) aiSwitchEnabled(ctx context.Context) bool {
	if s.opsConfig == nil {
		return s.cfg.AIEnabled
	}
	flags, err := s.opsConfig.Flags(ctx)
	if err != nil {
		return false
	}
	return flags.AIEnabled
}

func (s *Server) runtimeAI(ctx context.Context) (opsconfig.AIRuntime, error) {
	stored, err := s.runtimeAISettings(ctx)
	if err != nil {
		return opsconfig.AIRuntime{}, err
	}
	return opsconfig.ResolveAI(stored, s.deps.AICipher, s.aiFallback(), s.cfg.AllowPrivateAINetwork())
}

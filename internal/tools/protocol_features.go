package tools

import "sync"

type ProtocolFeatureProvider interface {
	ProtocolFeatureProvider()
}

type protocolFeatureSlot struct {
	mu       sync.Mutex
	provider ProtocolFeatureProvider
}

func (r *Runtime) ProtocolFeatures() ProtocolFeatureProvider {
	if r == nil {
		return nil
	}
	r.protocolFeatures.mu.Lock()
	defer r.protocolFeatures.mu.Unlock()
	return r.protocolFeatures.provider
}

func (r *Runtime) EnsureProtocolFeatures(candidate ProtocolFeatureProvider) ProtocolFeatureProvider {
	if r == nil || candidate == nil {
		return nil
	}
	r.protocolFeatures.mu.Lock()
	defer r.protocolFeatures.mu.Unlock()
	if r.protocolFeatures.provider == nil {
		r.protocolFeatures.provider = candidate
	}
	return r.protocolFeatures.provider
}

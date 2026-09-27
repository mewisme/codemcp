package approval

type Diagnostics struct {
	Available     bool `json:"available"`
	Challenges    int  `json:"challenges"`
	Requests      int  `json:"requests"`
	Pending       int  `json:"pending"`
	Approved      int  `json:"approved"`
	Denied        int  `json:"denied"`
	Expired       int  `json:"expired"`
	Cancelled     int  `json:"cancelled"`
	Consumed      int  `json:"consumed"`
	RuntimeGrants int  `json:"runtime_grants"`
	Stale         int  `json:"stale"`
}

func (m *Manager) Diagnostics() Diagnostics {
	if m == nil {
		return Diagnostics{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	result := Diagnostics{
		Available:     true,
		Challenges:    len(m.challenges),
		Requests:      len(m.requests),
		RuntimeGrants: len(m.runtimeGrants),
	}
	now := m.now().UTC()
	for _, record := range m.requests {
		if record == nil {
			continue
		}
		switch record.value.Status {
		case StatusPending:
			result.Pending++
		case StatusApproved:
			result.Approved++
		case StatusDenied:
			result.Denied++
		case StatusExpired:
			result.Expired++
		case StatusCancelled:
			result.Cancelled++
		case StatusConsumed:
			result.Consumed++
		}
		if !record.value.ExpiresAt.IsZero() && !now.Before(record.value.ExpiresAt) &&
			(record.value.Status == StatusPending || record.value.Status == StatusApproved) {
			result.Stale++
		}
	}
	return result
}

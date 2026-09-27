package secretstore

import (
	"errors"
	"strings"
)

type Diagnostics struct {
	Available  bool `json:"available"`
	Checked    int  `json:"checked"`
	Configured int  `json:"configured"`
	Missing    int  `json:"missing"`
	Failed     int  `json:"failed"`
}

func (s *Store) Inspect(names []string) Diagnostics {
	result := Diagnostics{}
	if s == nil || s.ready("inspect", "") != nil {
		result.Failed = len(names)
		return result
	}
	result.Available = true
	seen := map[string]struct{}{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result.Checked++
		_, err := s.Get(name)
		switch {
		case err == nil:
			result.Configured++
		case errors.Is(err, ErrNotFound):
			result.Missing++
		default:
			result.Failed++
		}
	}
	return result
}

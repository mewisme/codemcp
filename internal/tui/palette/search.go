package palette

import (
	"sort"
	"strings"
	"unicode"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/tui/action"
)

type Result struct {
	Action action.Action
	Score  int
}

func Rank(actions []action.Action, query string, ctx action.Context) []Result {
	return RankWithRecent(actions, query, ctx, nil)
}

func RankWithRecent(actions []action.Action, query string, ctx action.Context, recent []string) []Result {
	query = normalize(query)
	recentRank := make(map[string]int, len(recent))
	for index, id := range recent {
		if _, exists := recentRank[id]; !exists {
			recentRank[id] = index
		}
	}
	results := make([]Result, 0, len(actions))
	for _, item := range actions {
		score, ok := actionScore(item, query, ctx)
		if ok {
			if index, exists := recentRank[item.ID]; exists {
				if query == "" {
					score += max(1, 80-index*4)
				} else {
					score += max(1, 20-index)
				}
			}
			results = append(results, Result{Action: item, Score: score})
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		left := strings.ToLower(results[i].Action.Category + "\x00" + results[i].Action.Title + "\x00" + results[i].Action.ID)
		right := strings.ToLower(results[j].Action.Category + "\x00" + results[j].Action.Title + "\x00" + results[j].Action.ID)
		return left < right
	})
	return results
}

func actionScore(item action.Action, query string, ctx action.Context) (int, bool) {
	if query == "" {
		return contextScore(item, ctx), true
	}
	title := normalize(item.Title)
	category := normalize(item.Category)
	command := normalize(strings.Join(item.CommandPath, " "))
	capabilities := normalize(capability.SearchText(item.Capabilities))
	keywords := normalize(strings.Join(item.Keywords, " "))
	description := normalize(item.Description)
	tokens := strings.Fields(query)
	haystack := strings.Join([]string{title, category, command, capabilities, keywords, description}, " ")
	for _, token := range tokens {
		if !strings.Contains(haystack, token) && !subsequence(token, haystack) {
			return 0, false
		}
	}
	score := contextScore(item, ctx)
	if strings.HasPrefix(title, query) {
		score += 1000
	}
	if command == query || strings.HasPrefix(command, query) {
		score += 850
	}
	if strings.HasPrefix(category, query) {
		score += 550
	}
	if strings.Contains(title, query) {
		score += 400
	}
	if strings.Contains(command, query) {
		score += 350
	}
	if strings.Contains(capabilities, query) {
		score += 340
	}
	for _, token := range tokens {
		switch {
		case strings.HasPrefix(title, token):
			score += 160
		case strings.Contains(title, token):
			score += 120
		case strings.Contains(command, token):
			score += 100
		case strings.Contains(capabilities, token):
			score += 95
		case strings.Contains(keywords, token):
			score += 80
		case strings.Contains(category, token):
			score += 60
		default:
			score += 20
		}
	}
	return score, true
}

func contextScore(item action.Action, ctx action.Context) int {
	score := 0
	if ctx.Route != "" {
		route := normalize(ctx.Route)
		if normalize(item.Category) == route || strings.Contains(normalize(strings.Join(item.Keywords, " ")), route) {
			score += 120
		}
	}
	if ctx.ResourceID != "" {
		resource := normalize(ctx.ResourceID)
		haystack := normalize(strings.Join([]string{item.Title, item.Description, strings.Join(item.Keywords, " "), strings.Join(item.CommandPath, " ")}, " "))
		if strings.Contains(haystack, resource) {
			score += 300
		}
	}
	return score
}

func normalize(value string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(strings.TrimSpace(value)), func(r rune) bool {
		return unicode.IsSpace(r) || r == ':' || r == '/' || r == '_' || r == '-'
	}), " ")
}

func subsequence(needle, haystack string) bool {
	if needle == "" {
		return true
	}
	needleRunes := []rune(needle)
	index := 0
	for _, value := range haystack {
		if value == needleRunes[index] {
			index++
			if index == len(needleRunes) {
				return true
			}
		}
	}
	return false
}

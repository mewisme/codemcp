package mcp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"strings"
	"unicode/utf8"

	"go.mewis.me/codemcp/internal/tools"
)

const mcpParamHeaderPrefix = "Mcp-Param-"

type toolHeaderSpec struct {
	Argument string
	Header   string
	Type     string
}

func validateToolParamHeaders(r *http.Request, schema tools.Schema, args map[string]any) *Error {
	specs, err := toolHeaderSpecs(schema.InputSchema)
	if err != nil {
		return NewError(ErrHeaderMismatch, "invalid x-mcp-header schema: "+err.Error())
	}
	expected := make(map[string]toolHeaderSpec, len(specs))
	for _, spec := range specs {
		expected[strings.ToLower(spec.Header)] = spec
	}

	actual := map[string][]string{}
	for key, values := range r.Header {
		if strings.HasPrefix(strings.ToLower(key), strings.ToLower(mcpParamHeaderPrefix)) {
			actual[strings.ToLower(key)] = append([]string(nil), values...)
		}
	}

	for _, spec := range specs {
		key := strings.ToLower(spec.Header)
		values := actual[key]
		value, present := args[spec.Argument]
		if !present || value == nil {
			if len(values) != 0 {
				return NewError(ErrHeaderMismatch, fmt.Sprintf("%s header must be absent when argument %q is absent", spec.Header, spec.Argument))
			}
			delete(actual, key)
			continue
		}
		if len(values) != 1 {
			if len(values) == 0 {
				return NewError(ErrHeaderMismatch, fmt.Sprintf("missing %s header for argument %q", spec.Header, spec.Argument))
			}
			return NewError(ErrHeaderMismatch, fmt.Sprintf("%s header must appear exactly once", spec.Header))
		}
		decoded, err := decodeMCPHeaderValue(values[0])
		if err != nil {
			return NewError(ErrHeaderMismatch, fmt.Sprintf("malformed %s header: %v", spec.Header, err))
		}
		if err := compareToolHeaderValue(spec.Type, value, decoded); err != nil {
			return NewError(ErrHeaderMismatch, fmt.Sprintf("%s does not match argument %q: %v", spec.Header, spec.Argument, err))
		}
		delete(actual, key)
	}

	for key := range actual {
		if _, ok := expected[key]; !ok {
			return NewError(ErrHeaderMismatch, fmt.Sprintf("unexpected %s header", http.CanonicalHeaderKey(key)))
		}
	}
	return nil
}

func toolHeaderSpecs(raw json.RawMessage) ([]toolHeaderSpec, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var schema any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&schema); err != nil {
		return nil, fmt.Errorf("decode inputSchema: %w", err)
	}
	root, ok := schema.(map[string]any)
	if !ok {
		return nil, errors.New("inputSchema must be an object")
	}
	total := countMCPHeaderAnnotations(root)
	properties, ok := root["properties"].(map[string]any)
	if !ok {
		if total > 0 {
			return nil, errors.New("x-mcp-header is only supported on top-level inputSchema properties")
		}
		return nil, nil
	}

	specs := make([]toolHeaderSpec, 0)
	seen := map[string]bool{}
	topLevel := 0
	for argument, rawProperty := range properties {
		property, ok := rawProperty.(map[string]any)
		if !ok {
			continue
		}
		rawHeader, exists := property["x-mcp-header"]
		if !exists {
			continue
		}
		topLevel++
		header, ok := rawHeader.(string)
		if !ok || header == "" || !isHTTPToken(header) {
			return nil, fmt.Errorf("invalid x-mcp-header for %s", argument)
		}
		key := strings.ToLower(header)
		if seen[key] {
			return nil, fmt.Errorf("duplicate x-mcp-header %q", header)
		}
		seen[key] = true
		kind, _ := property["type"].(string)
		switch kind {
		case "string", "integer", "boolean":
		default:
			return nil, fmt.Errorf("x-mcp-header parameter %s must use string, integer, or boolean type", argument)
		}
		specs = append(specs, toolHeaderSpec{Argument: argument, Header: mcpParamHeaderPrefix + header, Type: kind})
	}
	if total != topLevel {
		return nil, errors.New("x-mcp-header is only supported on top-level inputSchema properties")
	}
	return specs, nil
}

func countMCPHeaderAnnotations(value any) int {
	switch typed := value.(type) {
	case map[string]any:
		count := 0
		for key, item := range typed {
			if key == "x-mcp-header" {
				count++
			}
			count += countMCPHeaderAnnotations(item)
		}
		return count
	case []any:
		count := 0
		for _, item := range typed {
			count += countMCPHeaderAnnotations(item)
		}
		return count
	default:
		return 0
	}
}

func HeaderSafeTool(schema tools.Schema) bool {
	_, err := toolHeaderSpecs(schema.InputSchema)
	return err == nil
}

func filterHeaderSafeTools(values []tools.Schema) []tools.Schema {
	out := make([]tools.Schema, 0, len(values))
	for _, schema := range values {
		if HeaderSafeTool(schema) {
			out = append(out, schema)
		}
	}
	return out
}

func compareToolHeaderValue(kind string, body any, header string) error {
	switch kind {
	case "string":
		text, ok := body.(string)
		if !ok {
			return errors.New("body value is not a string")
		}
		if text != header {
			return errors.New("string values differ")
		}
		return nil
	case "boolean":
		value, ok := body.(bool)
		if !ok {
			return errors.New("body value is not a boolean")
		}
		want := "false"
		if value {
			want = "true"
		}
		if header != want {
			return errors.New("boolean values differ")
		}
		return nil
	case "integer":
		bodyInteger, err := normalizeInteger(body)
		if err != nil {
			return err
		}
		headerInteger, err := normalizeIntegerString(header)
		if err != nil {
			return errors.New("header is not an integer")
		}
		if bodyInteger.Cmp(headerInteger) != 0 {
			return errors.New("integer values differ")
		}
		return nil
	default:
		return fmt.Errorf("unsupported x-mcp-header type %q", kind)
	}
}

func normalizeInteger(value any) (*big.Int, error) {
	switch typed := value.(type) {
	case json.Number:
		return normalizeIntegerString(typed.String())
	case int:
		return big.NewInt(int64(typed)), nil
	case int8:
		return big.NewInt(int64(typed)), nil
	case int16:
		return big.NewInt(int64(typed)), nil
	case int32:
		return big.NewInt(int64(typed)), nil
	case int64:
		return big.NewInt(typed), nil
	case uint:
		return new(big.Int).SetUint64(uint64(typed)), nil
	case uint8:
		return new(big.Int).SetUint64(uint64(typed)), nil
	case uint16:
		return new(big.Int).SetUint64(uint64(typed)), nil
	case uint32:
		return new(big.Int).SetUint64(uint64(typed)), nil
	case uint64:
		return new(big.Int).SetUint64(typed), nil
	case float32:
		return normalizeIntegerFloat(float64(typed))
	case float64:
		return normalizeIntegerFloat(typed)
	default:
		return nil, errors.New("body value is not an integer")
	}
}

func normalizeIntegerFloat(value float64) (*big.Int, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value {
		return nil, errors.New("body value is not an integer")
	}
	result, accuracy := new(big.Float).SetFloat64(value).Int(nil)
	if accuracy != big.Exact {
		return nil, errors.New("body value is not an exact integer")
	}
	return result, nil
}

func normalizeIntegerString(value string) (*big.Int, error) {
	rational, ok := new(big.Rat).SetString(value)
	if !ok || rational.Denom().Cmp(big.NewInt(1)) != 0 {
		return nil, errors.New("not an integer")
	}
	return new(big.Int).Set(rational.Num()), nil
}

func decodeMCPHeaderValue(value string) (string, error) {
	if strings.HasPrefix(value, "=?base64?") || strings.HasSuffix(value, "?=") {
		if !strings.HasPrefix(value, "=?base64?") || !strings.HasSuffix(value, "?=") {
			return "", errors.New("invalid base64 sentinel")
		}
		payload := strings.TrimSuffix(strings.TrimPrefix(value, "=?base64?"), "?=")
		if payload == "" {
			return "", errors.New("empty base64 sentinel")
		}
		data, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return "", errors.New("invalid base64 sentinel")
		}
		if !utf8.Valid(data) {
			return "", errors.New("base64 sentinel is not UTF-8")
		}
		return string(data), nil
	}
	if !safeMCPHeaderValue(value) {
		return "", errors.New("unsafe raw header value")
	}
	return value, nil
}

func safeMCPHeaderValue(value string) bool {
	if len(value) > 0 {
		if value[0] == ' ' || value[0] == '\t' || value[len(value)-1] == ' ' || value[len(value)-1] == '\t' {
			return false
		}
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if char == '\t' {
			continue
		}
		if char < 0x20 || char > 0x7e {
			return false
		}
	}
	return true
}

func isHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
			continue
		}
		if !strings.ContainsRune("!#$%&'*+-.^_`|~", rune(char)) {
			return false
		}
	}
	return true
}

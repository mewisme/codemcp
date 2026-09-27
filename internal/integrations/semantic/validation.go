package semantic

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
)

func ValidateRequest(request Request) error {
	if err := validateConsumer(request.Consumer); err != nil {
		return invalidRequest(err)
	}
	if request.State == nil {
		return invalidRequest(errors.New("semantic state is required"))
	}
	if err := validateJSONValue("semantic state", request.State, MaxStateBytes); err != nil {
		return invalidRequest(err)
	}
	if len(request.Questions) == 0 {
		return invalidRequest(errors.New("semantic questions are required"))
	}
	if len(request.Questions) > MaxQuestions {
		return invalidRequest(fmt.Errorf("semantic question count %d exceeds maximum %d", len(request.Questions), MaxQuestions))
	}
	for id, question := range request.Questions {
		if err := validateSafeMetadata("question id", id, MaxQuestionIDBytes); err != nil {
			return invalidRequest(err)
		}
		if err := ValidateQuestion(question); err != nil {
			return invalidRequest(fmt.Errorf("semantic question %q: %w", id, err))
		}
	}
	return nil
}

func ValidateQuestion(question Question) error {
	if question.Instructions == nil {
		return errors.New("instructions are required")
	}
	if err := validateJSONValue("instructions", question.Instructions, MaxStateBytes); err != nil {
		return err
	}
	switch question.Type {
	case PrimitiveNoul:
		if question.Choice != nil || question.Score != nil {
			return errors.New("noul question must define only noul criteria")
		}
		if question.Noul != nil && question.Noul.True != nil {
			if err := validateJSONValue("noul true criteria", question.Noul.True, MaxStateBytes); err != nil {
				return err
			}
		}
		if question.Noul != nil && question.Noul.False != nil {
			if err := validateJSONValue("noul false criteria", question.Noul.False, MaxStateBytes); err != nil {
				return err
			}
		}
	case PrimitiveChoice:
		if question.Noul != nil || question.Score != nil {
			return errors.New("choice question must define only choice criteria")
		}
		if len(question.Choice) < 2 || len(question.Choice) > MaxCriteriaEntries {
			return fmt.Errorf("choice criteria count must be between 2 and %d", MaxCriteriaEntries)
		}
		for key, description := range question.Choice {
			if err := validateSafeMetadata("choice criterion", key, MaxQuestionIDBytes); err != nil {
				return err
			}
			if description != nil {
				if err := validateJSONValue("choice criterion description", description, MaxStateBytes); err != nil {
					return err
				}
			}
		}
	case PrimitiveScore:
		if question.Noul != nil || question.Choice != nil {
			return errors.New("score question must define only score criteria")
		}
		if len(question.Score) < 2 || len(question.Score) > MaxScoreLevels {
			return fmt.Errorf("score criteria count must be between 2 and %d", MaxScoreLevels)
		}
		seen := map[string]bool{}
		last := math.Inf(-1)
		for _, level := range question.Score {
			if err := validateSafeMetadata("score criterion", level.Key, MaxQuestionIDBytes); err != nil {
				return err
			}
			if seen[level.Key] {
				return fmt.Errorf("score criterion key %q is duplicated", level.Key)
			}
			seen[level.Key] = true
			if !finite(level.Value) || level.Value <= last {
				return errors.New("score criterion values must be finite and strictly increasing")
			}
			last = level.Value
			if level.Description == nil {
				return errors.New("score criterion description is required")
			}
			if err := validateJSONValue("score criterion description", level.Description, MaxStateBytes); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported semantic primitive %q", question.Type)
	}
	return nil
}

func ValidateResult(request Request, result Result) error {
	if err := ValidateRequest(request); err != nil {
		return err
	}
	if len(result.Answers) != len(request.Questions) {
		return NewError(ErrorInvalidResponse, "answer set does not match requested questions")
	}
	for id, question := range request.Questions {
		answer, ok := result.Answers[id]
		if !ok {
			return NewError(ErrorInvalidResponse, "required answer is missing")
		}
		if err := validateAnswer(question, answer); err != nil {
			return NewError(ErrorInvalidResponse, fmt.Sprintf("semantic answer %q: %v", id, err))
		}
	}
	if err := ValidateProviderMetadata(result.ProviderMetadata); err != nil {
		return NewError(ErrorInvalidResponse, err.Error())
	}
	return nil
}

func ValidateProviderMetadata(metadata ProviderMetadata) error {
	if err := validateSafeMetadata("provider", metadata.Provider, MaxProviderIDBytes); err != nil {
		return err
	}
	if metadata.Model != "" {
		if err := validateSafeMetadata("model", metadata.Model, MaxModelIDBytes); err != nil {
			return err
		}
	}
	if metadata.Duration < 0 {
		return errors.New("provider duration must be non-negative")
	}
	if metadata.Usage != nil && (metadata.Usage.InputTokens < 0 || metadata.Usage.OutputTokens < 0) {
		return errors.New("usage token counts must be non-negative")
	}
	return nil
}

func ValidateRiskInput(input RiskInput) error {
	if err := validateConsumer(input.Consumer); err != nil {
		return invalidRequest(err)
	}
	if input.WorkspaceID != "" {
		if err := validateSafeMetadata("workspace id", input.WorkspaceID, MaxConsumerIDBytes); err != nil {
			return invalidRequest(err)
		}
	}
	if input.CallerID != "" {
		if err := validateSafeMetadata("caller id", input.CallerID, MaxConsumerIDBytes); err != nil {
			return invalidRequest(err)
		}
	}
	if err := validateSafeMetadata("canonical operation", input.Invocation.Operation, MaxQuestionIDBytes); err != nil {
		return invalidRequest(err)
	}
	if input.Invocation.Tool != "" {
		if err := validateSafeMetadata("canonical tool", input.Invocation.Tool, MaxQuestionIDBytes); err != nil {
			return invalidRequest(err)
		}
	}
	if input.Invocation.Arguments != nil {
		if err := validateJSONValue("canonical invocation arguments", input.Invocation.Arguments, MaxRiskInputBytes); err != nil {
			return invalidRequest(err)
		}
	}
	return nil
}

func ValidateRiskAssessment(input RiskInput, assessment RiskAssessment, minConfidence float64) error {
	if err := ValidateRiskInput(input); err != nil {
		return err
	}
	if !probability(minConfidence) {
		return invalidRequest(errors.New("minimum risk confidence must be between 0 and 1"))
	}
	switch assessment.Class {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
	default:
		return NewError(ErrorInvalidResponse, "unknown semantic risk class")
	}
	if !probability(assessment.Confidence) {
		return NewError(ErrorInvalidResponse, "risk confidence is out of range")
	}
	if assessment.Confidence < minConfidence {
		return NewError(ErrorInvalidResponse, "risk confidence is below required minimum")
	}
	if assessment.Category != "" {
		if err := validateSafeMetadata("risk category", assessment.Category, MaxRiskCategoryBytes); err != nil {
			return NewError(ErrorInvalidResponse, err.Error())
		}
	}
	if err := validateBoundedText("risk reason", assessment.Reason, MaxRiskReasonBytes); err != nil {
		return NewError(ErrorInvalidResponse, err.Error())
	}
	if err := ValidateProviderMetadata(assessment.Provider); err != nil {
		return NewError(ErrorInvalidResponse, err.Error())
	}
	return nil
}

func validateAnswer(question Question, answer Answer) error {
	if answer.Type != question.Type {
		return errors.New("answer primitive does not match question")
	}
	switch answer.Type {
	case PrimitiveNoul:
		if answer.Noul == nil || answer.Choice != nil || answer.Score != nil || !probability(answer.Noul.ProbabilityYes) {
			return errors.New("invalid noul answer")
		}
	case PrimitiveChoice:
		if answer.Choice == nil || answer.Noul != nil || answer.Score != nil {
			return errors.New("invalid choice answer")
		}
		if _, ok := question.Choice[answer.Choice.Choice]; !ok {
			return errors.New("choice answer selects unknown criterion")
		}
		if !probability(answer.Choice.Confidence) {
			return errors.New("choice confidence is out of range")
		}
		if err := validateDistribution(answer.Choice.Probabilities, question.Choice); err != nil {
			return err
		}
	case PrimitiveScore:
		if answer.Score == nil || answer.Noul != nil || answer.Choice != nil || !finite(answer.Score.Score) || !probability(answer.Score.Confidence) {
			return errors.New("invalid score answer")
		}
		criteria := make(map[string]any, len(question.Score))
		minValue, maxValue := question.Score[0].Value, question.Score[len(question.Score)-1].Value
		if answer.Score.Score < minValue || answer.Score.Score > maxValue {
			return errors.New("score answer is outside rubric range")
		}
		for _, level := range question.Score {
			criteria[level.Key] = nil
		}
		if err := validateDistribution(answer.Score.Probabilities, criteria); err != nil {
			return err
		}
	default:
		return errors.New("unsupported answer primitive")
	}
	return nil
}

func validateDistribution(values map[string]float64, criteria map[string]any) error {
	if len(values) != len(criteria) {
		return errors.New("probability distribution does not cover all criteria")
	}
	total := 0.0
	for key := range criteria {
		value, ok := values[key]
		if !ok || !probability(value) {
			return errors.New("probability distribution contains invalid criterion probability")
		}
		total += value
	}
	if math.Abs(total-1) > ProbabilityEpsilon {
		return errors.New("probability distribution must sum approximately to 1")
	}
	return nil
}

func validateConsumer(consumer Consumer) error {
	if err := validateSafeMetadata("consumer id", consumer.ID, MaxConsumerIDBytes); err != nil {
		return err
	}
	return validateSafeMetadata("consumer purpose", consumer.Purpose, MaxPurposeBytes)
}

func validateSafeMetadata(name, value string, maxBytes int) error {
	if value == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%s is empty or not normalized", name)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("%s exceeds maximum %d bytes", name, maxBytes)
	}
	for _, r := range value {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' || r == ':' || r == '/') {
			return fmt.Errorf("%s contains unsafe metadata character", name)
		}
	}
	return nil
}

func validateBoundedText(name, value string, maxBytes int) error {
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("%s is not normalized", name)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("%s exceeds maximum %d bytes", name, maxBytes)
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return fmt.Errorf("%s contains control characters", name)
		}
	}
	return nil
}

func validateJSONValue(name string, value any, maxBytes int) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%s is not JSON-compatible: %w", name, err)
	}
	if len(data) > maxBytes {
		return fmt.Errorf("%s encoded size %d exceeds maximum %d bytes", name, len(data), maxBytes)
	}
	var normalized any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&normalized); err != nil {
		return fmt.Errorf("%s is not JSON-compatible: %w", name, err)
	}
	return validateFiniteJSON(name, normalized)
}

func validateFiniteJSON(name string, value any) error {
	switch current := value.(type) {
	case map[string]any:
		for _, child := range current {
			if err := validateFiniteJSON(name, child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range current {
			if err := validateFiniteJSON(name, child); err != nil {
				return err
			}
		}
	case json.Number:
		value, err := current.Float64()
		if err != nil || !finite(value) {
			return fmt.Errorf("%s contains invalid numeric value", name)
		}
	}
	return nil
}

func probability(value float64) bool { return finite(value) && value >= 0 && value <= 1 }
func finite(value float64) bool      { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func invalidRequest(err error) error {
	if err == nil {
		return nil
	}
	return NewError(ErrorInvalidRequest, err.Error())
}

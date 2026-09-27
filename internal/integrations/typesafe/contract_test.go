package typesafe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestVerifiedContractConstants(t *testing.T) {
	if VerifiedAt != "2026-09-27" {
		t.Fatalf("verification date changed without re-verifying live TypeSafe docs: %q", VerifiedAt)
	}
	if DefaultBaseURL != "https://api.typesafe.ai" || SystemOnePath != "/v1/systemone" || ModelsPath != "/v1/models" {
		t.Fatalf("endpoint contract drift: %q %q %q", DefaultBaseURL, SystemOnePath, ModelsPath)
	}
	if DefaultModel != "jev-latest" || VerifiedStableModel != "jev-1.13.0" {
		t.Fatalf("model contract drift: default=%q stable=%q", DefaultModel, VerifiedStableModel)
	}
	if MaxChoiceOptions != 255 || MaxScoreLevels != 10 || MaxContextTokens != 64_000 || MaxStateLongestQTokens != 32_000 {
		t.Fatal("verified provider limits changed without contract review")
	}
	if SDKDefaultTimeout != 10*time.Second {
		t.Fatalf("SDK timeout contract=%s", SDKDefaultTimeout)
	}
	if RiskClassifierAvailable {
		t.Fatal("generic Jev primitives must not be promoted to a dedicated risk classifier")
	}
}

func TestCapturedSystemOneFixturesMatchVerifiedWireShape(t *testing.T) {
	requestData := readFixture(t, "systemone_request.json")
	var request SystemOneRequest
	if err := json.Unmarshal(requestData, &request); err != nil {
		t.Fatal(err)
	}
	if request.Model != DefaultModel || len(request.Questions) != 3 {
		t.Fatalf("request=%#v", request)
	}
	if request.Questions["urgent"].Type != "noul" ||
		request.Questions["route"].Type != "choice" ||
		request.Questions["severity"].Type != "score" {
		t.Fatalf("question primitives=%#v", request.Questions)
	}

	responseData := readFixture(t, "systemone_response.json")
	var response SystemOneResponse
	if err := json.Unmarshal(responseData, &response); err != nil {
		t.Fatal(err)
	}
	if response.Model != VerifiedStableModel || len(response.Answers) != 3 {
		t.Fatalf("response=%#v", response)
	}
	if answer := response.Answers["urgent"]; answer.Type != "noul" || answer.Noul == nil || *answer.Noul != 0.95 {
		t.Fatalf("noul=%#v", answer)
	}
	if answer := response.Answers["route"]; answer.Type != "choice" || answer.Choice == nil || *answer.Choice != "billing" || answer.Confidence == nil {
		t.Fatalf("choice=%#v", answer)
	}
	if answer := response.Answers["severity"]; answer.Type != "score" || answer.Score == nil || answer.Confidence == nil || len(answer.Legend) != 3 {
		t.Fatalf("score=%#v", answer)
	}
	if response.Usage.InputTokens <= 0 || response.Usage.OutputTokens <= 0 {
		t.Fatalf("usage=%#v", response.Usage)
	}
}

func TestCapturedModelsFixtureMatchesProbeShape(t *testing.T) {
	data := readFixture(t, "models_response.json")
	var response ModelsResponse
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	want := []string{"jev-latest", "jev-preview"}
	got := make([]string, 0, len(response.Models))
	for _, model := range response.Models {
		got = append(got, model.Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("models=%v want=%v", got, want)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

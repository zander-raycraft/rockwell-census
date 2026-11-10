package tests

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"census/src/extractor"
	"census/src/parse"
)

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type capturedChatRequest struct {
	Model           string        `json:"model"`
	ReasoningEffort string        `json:"reasoning_effort"`
	Temperature     *float64      `json:"temperature"`
	Messages        []chatMessage `json:"messages"`
}

type extractionTransport struct {
	request capturedChatRequest
}

/*
RoundTrip captures the model request and returns a fixed email plan without network access.

* @brief: RoundTrip -> supplies an offline model response
* @params: request [*http.Request] -> request whose JSON body is captured
* @returns: response [*http.Response] -> fixed successful extraction response
* @returns: err [error] -> non-nil when request JSON cannot be read
*/
func (transport *extractionTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	defer request.Body.Close()
	if err := json.NewDecoder(request.Body).Decode(&transport.request); err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"{\"document_type\":\"email\",\"date_format\":\"MIXED\",\"mappings\":[],\"labels\":[],\"uncertainties\":[],\"email_resolution\":null}"}}]}`)),
		Header:     make(http.Header),
	}, nil
}

/*
TestExtractReasoningParameters checks request compatibility without calling a live model.

* @brief: TestExtractReasoningParameters -> omits temperature for reasoning and preserves zero for none
* @params: t [*testing.T] -> test runner
*/
func TestExtractReasoningParameters(t *testing.T) {
	for _, effort := range []string{"medium", "none"} {
		t.Run(effort, func(t *testing.T) {
			transport := &extractionTransport{}
			client := extractor.NewOpenAIExtractor("test", "test-model", &http.Client{Transport: transport})
			client.SetReasoningEffort(effort)
			_, _, err := client.Extract(context.Background(), parse.Parsed{Kind: "email"}, "")
			if err != nil {
				t.Fatalf("Extract(%s) = %v; want nil", effort, err)
			}
			request := transport.request
			if request.Model != "test-model" || request.ReasoningEffort != effort {
				t.Errorf("Extract(%s) model/effort = %q/%q; want test-model/%s", effort, request.Model, request.ReasoningEffort, effort)
			}
			if (request.Temperature != nil) != (effort == "none") {
				t.Errorf("Extract(%s) temperature = %v; want present only for none", effort, request.Temperature)
			}
			if request.Temperature != nil && *request.Temperature != 0 {
				t.Errorf("Extract(%s) temperature = %v; want 0", effort, *request.Temperature)
			}
		})
	}
}

/*
TestExtractionUserPromptKeepsTableMappingIndependent prevents previous issues from changing header interpretation.

* @brief: TestExtractionUserPromptKeepsTableMappingIndependent -> sends issue context only for email replies
* @params: t [*testing.T] -> test runner
*/
func TestExtractionUserPromptKeepsTableMappingIndependent(t *testing.T) {
	for _, kind := range []string{"csv", "email"} {
		transport := &extractionTransport{}
		client := extractor.NewOpenAIExtractor("test", "test-model", &http.Client{Transport: transport})
		_, _, err := client.Extract(context.Background(), parse.Parsed{Kind: kind, Preview: "document preview"}, "- SSN_CONFLICT: prior conflict context\n")
		if err != nil {
			t.Fatalf("Extract(%s) = %v; want nil", kind, err)
		}
		prompt := transport.request.Messages[1].Content
		if strings.Contains(prompt, "prior conflict context") != (kind == "email") {
			t.Errorf("extractionUserPrompt(%s) = %q; want prior issues only for email", kind, prompt)
		}
	}
}

package apiserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	kaf "github.com/HariKube/kubernetes-aggregator-framework/pkg/framework"
	"go.yaml.in/yaml/v2"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const defaultDecisionMakerURL = "http://127.0.0.1:8088/system-one"

const defaultDecisionMakerTimeout = 30 * time.Second

func DefaultDecisionMakerURL() string {
	return defaultDecisionMakerURL
}

func DefaultDecisionMakerTimeout() time.Duration {
	return defaultDecisionMakerTimeout
}

var (
	decisionLogger              = logf.Log.WithName("api-extension.decision")
	decisionSubjectAccessReview = subjectAccessReview
	invokeDecisionMaker         = invokeDecisionMakerHTTP
)

type decisionQuestion struct {
	Type         string      `yaml:"type" json:"type"`
	Instructions string      `yaml:"instructions" json:"instructions"`
	Criteria     interface{} `yaml:"criteria,omitempty" json:"criteria,omitempty"`
}

type decisionRequest struct {
	Name      string
	Namespace string
	State     interface{}
	Questions map[string]decisionQuestion
}

type decisionSystemOneRequest struct {
	State     interface{}                 `json:"state"`
	Questions map[string]decisionQuestion `json:"questions"`
}

type decisionSystemOneResponse struct {
	Answers map[string]interface{} `json:"answers"`
	Usage   map[string]interface{} `json:"usage,omitempty"`
}

type decisionAuthorizer func(context.Context, *authorizationv1.ResourceAttributes, http.Header) (*authorizationv1.SubjectAccessReview, error)

type decisionHandlerConfig struct {
	authorize            decisionAuthorizer
	decisionMakerURL     string
	decisionMakerTimeout time.Duration
}

func getDecisionHandler(config decisionHandlerConfig) *kaf.APIKind {
	endpoint := strings.TrimSpace(config.decisionMakerURL)
	if endpoint == "" {
		endpoint = defaultDecisionMakerURL
	}
	if config.decisionMakerTimeout <= 0 {
		config.decisionMakerTimeout = defaultDecisionMakerTimeout
	}

	return &kaf.APIKind{
		ApiResource: metav1.APIResource{
			Name:         "decisionrequests",
			SingularName: "decisionrequest",
			Namespaced:   true,
			Kind:         "DecisionRequest",
			Verbs:        []string{"create"},
		},
		CustomResource: &kaf.CustomResource{
			CreateHandler: func(namespace, name string, w http.ResponseWriter, r *http.Request) {
				ctx, cancel := context.WithTimeout(r.Context(), config.decisionMakerTimeout)
				defer cancel()

				if result, err := config.authorize(ctx,
					&authorizationv1.ResourceAttributes{
						Namespace: namespace,
						Verb:      "create",
						Group:     Group,
						Resource:  "decisionrequests",
					}, r.Header); err != nil {
					http.Error(w, "resource not found", http.StatusNotFound)
					return
				} else if !result.Status.Allowed {
					http.Error(w, "resource forbidden", http.StatusForbidden)
					return
				}

				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				if len(strings.TrimSpace(string(body))) == 0 {
					http.Error(w, "empty body", http.StatusBadRequest)
					return
				}

				request, err := decodeDecisionRequest(body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}

				requestNamespace := namespace
				if requestNamespace == "" {
					requestNamespace = transactionRequestNamespace(r.URL.Path)
				}
				if request.Namespace != "" && requestNamespace != "" && request.Namespace != requestNamespace {
					http.Error(w, "metadata.namespace does not match request namespace", http.StatusBadRequest)
					return
				}
				if requestNamespace == "" {
					requestNamespace = request.Namespace
				}

				decisionName := request.Name
				if decisionName == "" {
					decisionName = name
				}
				if decisionName == "" {
					decisionName = fmt.Sprintf("decision-%d", time.Now().UnixNano())
				}

				response, err := invokeDecisionMaker(ctx, endpoint, &decisionSystemOneRequest{
					State:     request.State,
					Questions: request.Questions,
				})
				if err != nil {
					decisionLogger.Error(err, "decision maker call failed", "endpoint", endpoint, "name", decisionName, "namespace", requestNamespace)
					http.Error(w, err.Error(), http.StatusBadGateway)
					return
				}

				contentType, _ := responseContent(r.Header)
				container := map[string]interface{}{
					"apiVersion": Group + "/" + Version,
					"kind":       "DecisionResponse",
					"metadata": map[string]interface{}{
						"name":              decisionName,
						"namespace":         requestNamespace,
						"creationTimestamp": metav1.Now().Format(time.RFC3339),
					},
					"spec": map[string]interface{}{
						"answers": response.Answers,
						"usage":   response.Usage,
					},
				}

				if err := writeResponse(w, http.StatusCreated, container, contentType); err != nil {
					decisionLogger.Info("Write error", "error", err)
				}
			},
		},
	}
}

func decodeDecisionRequest(body []byte) (*decisionRequest, error) {
	var payload struct {
		Metadata map[string]interface{} `yaml:"metadata"`
		Spec     struct {
			State     interface{}                 `yaml:"state"`
			Questions map[string]decisionQuestion `yaml:"questions"`
		} `yaml:"spec"`
	}

	if err := yaml.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if len(payload.Spec.Questions) == 0 {
		return nil, fmt.Errorf("spec.questions is required")
	}

	request := &decisionRequest{
		State:     normalizeDecisionValue(payload.Spec.State),
		Questions: map[string]decisionQuestion{},
	}
	if payload.Metadata != nil {
		if name, ok := payload.Metadata["name"].(string); ok {
			request.Name = strings.TrimSpace(name)
		}
		if namespace, ok := payload.Metadata["namespace"].(string); ok {
			request.Namespace = strings.TrimSpace(namespace)
		}
	}

	for key, question := range payload.Spec.Questions {
		if strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("spec.questions contains an empty key")
		}
		normalized, err := normalizeDecisionQuestion(key, question)
		if err != nil {
			return nil, err
		}
		request.Questions[key] = normalized
	}

	return request, nil
}

func normalizeDecisionQuestion(name string, question decisionQuestion) (decisionQuestion, error) {
	question.Type = strings.ToLower(strings.TrimSpace(question.Type))
	question.Instructions = strings.TrimSpace(question.Instructions)
	if question.Type == "" {
		return decisionQuestion{}, fmt.Errorf("spec.questions.%s.type is required", name)
	}
	if question.Instructions == "" {
		return decisionQuestion{}, fmt.Errorf("spec.questions.%s.instructions is required", name)
	}

	switch question.Type {
	case "choice":
		criteria, ok := normalizeDecisionValue(question.Criteria).(map[string]interface{})
		if !ok || len(criteria) == 0 {
			return decisionQuestion{}, fmt.Errorf("spec.questions.%s.criteria must be a non-empty object for choice questions", name)
		}
		normalizedCriteria := make(map[string]string, len(criteria))
		for option, raw := range criteria {
			text, ok := raw.(string)
			if !ok || strings.TrimSpace(option) == "" || strings.TrimSpace(text) == "" {
				return decisionQuestion{}, fmt.Errorf("spec.questions.%s.criteria must only contain non-empty string values", name)
			}
			normalizedCriteria[option] = strings.TrimSpace(text)
		}
		question.Criteria = normalizedCriteria
	case "score":
		rawCriteria, ok := normalizeDecisionValue(question.Criteria).([]interface{})
		if !ok || len(rawCriteria) == 0 {
			return decisionQuestion{}, fmt.Errorf("spec.questions.%s.criteria must be a non-empty array for score questions", name)
		}
		normalizedCriteria := make([]string, 0, len(rawCriteria))
		for _, raw := range rawCriteria {
			text, ok := raw.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return decisionQuestion{}, fmt.Errorf("spec.questions.%s.criteria must only contain non-empty strings", name)
			}
			normalizedCriteria = append(normalizedCriteria, strings.TrimSpace(text))
		}
		question.Criteria = normalizedCriteria
	case "noul":
		question.Criteria = nil
	default:
		return decisionQuestion{}, fmt.Errorf("spec.questions.%s.type must be one of choice, score, noul", name)
	}

	return question, nil
}

func normalizeDecisionValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[interface{}]interface{}:
		normalized := make(map[string]interface{}, len(typed))
		for key, entry := range typed {
			normalized[fmt.Sprint(key)] = normalizeDecisionValue(entry)
		}
		return normalized
	case map[string]interface{}:
		normalized := make(map[string]interface{}, len(typed))
		for key, entry := range typed {
			normalized[key] = normalizeDecisionValue(entry)
		}
		return normalized
	case []interface{}:
		normalized := make([]interface{}, 0, len(typed))
		for _, entry := range typed {
			normalized = append(normalized, normalizeDecisionValue(entry))
		}
		return normalized
	default:
		return typed
	}
}

func invokeDecisionMakerHTTP(ctx context.Context, endpoint string, request *decisionSystemOneRequest) (*decisionSystemOneResponse, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal decision request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build decision maker request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", contentTypeJSON)
	httpRequest.Header.Set("Accept", contentTypeJSON)

	httpResponse, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("call decision maker: %w", err)
	}
	defer func() {
		if err := httpResponse.Body.Close(); err != nil {
			decisionLogger.Info("Close error", "error", err)
		}
	}()

	responseBody, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return nil, fmt.Errorf("read decision maker response: %w", err)
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		message := strings.TrimSpace(string(responseBody))
		if message == "" {
			message = http.StatusText(httpResponse.StatusCode)
		}
		return nil, fmt.Errorf("decision maker returned %d: %s", httpResponse.StatusCode, message)
	}

	var response decisionSystemOneResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("decode decision maker response: %w", err)
	}
	if len(response.Answers) == 0 {
		return nil, fmt.Errorf("decision maker returned no answers")
	}

	return &response, nil
}

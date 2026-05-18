package google

import "encoding/json"

// Wire types mirror the Gemini REST API at
// /v1beta/models/{model}:generateContent. They are intentionally kept
// separate from galdor's shared schema so changes to the wire format
// never leak upward.

type generateRequest struct {
	Contents          []wireContent      `json:"contents"`
	SystemInstruction *wireContent       `json:"systemInstruction,omitempty"`
	Tools             []wireTool         `json:"tools,omitempty"`
	ToolConfig        *wireToolConfig    `json:"toolConfig,omitempty"`
	GenerationConfig  *wireGenerationCfg `json:"generationConfig,omitempty"`
	SafetySettings    []wireSafety       `json:"safetySettings,omitempty"`
	CachedContent     string             `json:"cachedContent,omitempty"`
}

// wireContent is one turn in the conversation. Gemini uses "user" and
// "model" as roles; the systemInstruction field at the request level
// carries the system prompt instead of a role.
type wireContent struct {
	Role  string     `json:"role,omitempty"`
	Parts []wirePart `json:"parts"`
}

// wirePart encodes the variants of a content part. Only the fields
// relevant to the active variant are populated.
type wirePart struct {
	// Text content.
	Text string `json:"text,omitempty"`

	// Inline binary data (images, audio, etc.).
	InlineData *wireBlob `json:"inlineData,omitempty"`

	// Reference to an uploaded file (not used directly by this adapter;
	// kept for response decoding).
	FileData *wireFileData `json:"fileData,omitempty"`

	// Function call emitted by the model.
	FunctionCall *wireFunctionCall `json:"functionCall,omitempty"`

	// Function response from the caller back to the model.
	FunctionResponse *wireFunctionResponse `json:"functionResponse,omitempty"`

	// Thought summary part (Gemini 2.5 thinking models). Surfaced in
	// responses but not modeled in galdor's schema yet.
	Thought bool `json:"thought,omitempty"`
}

type wireBlob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64
}

type wireFileData struct {
	MimeType string `json:"mimeType"`
	FileURI  string `json:"fileUri"`
}

type wireFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type wireFunctionResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type wireTool struct {
	FunctionDeclarations []wireFuncDecl `json:"functionDeclarations,omitempty"`
}

type wireFuncDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type wireToolConfig struct {
	FunctionCallingConfig *wireFCCfg `json:"functionCallingConfig,omitempty"`
}

type wireFCCfg struct {
	Mode                 string   `json:"mode,omitempty"` // "AUTO", "ANY", "NONE"
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type wireGenerationCfg struct {
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"topP,omitempty"`
	TopK             *int            `json:"topK,omitempty"`
	MaxOutputTokens  *int            `json:"maxOutputTokens,omitempty"`
	StopSequences    []string        `json:"stopSequences,omitempty"`
	ResponseMIMEType string          `json:"responseMimeType,omitempty"`
	ResponseSchema   json.RawMessage `json:"responseSchema,omitempty"`
}

type wireSafety struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// generateResponse is the body of a successful non-streaming call.
type generateResponse struct {
	Candidates     []wireCandidate     `json:"candidates"`
	UsageMetadata  wireUsage           `json:"usageMetadata"`
	ModelVersion   string              `json:"modelVersion"`
	PromptFeedback *wirePromptFeedback `json:"promptFeedback,omitempty"`
}

type wireCandidate struct {
	Content       wireContent  `json:"content"`
	FinishReason  string       `json:"finishReason"`
	SafetyRatings []wireRating `json:"safetyRatings,omitempty"`
	Index         int          `json:"index"`
}

type wirePromptFeedback struct {
	BlockReason   string       `json:"blockReason,omitempty"`
	SafetyRatings []wireRating `json:"safetyRatings,omitempty"`
}

type wireRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
	Blocked     bool   `json:"blocked,omitempty"`
}

type wireUsage struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"`
}

// errorResponse is the shape Google APIs return on 4xx/5xx.
//
// Documented at
// https://cloud.google.com/apis/design/errors#error_model
//
// Details often carries the canonical reason (e.g. "API_KEY_INVALID")
// when the status field is too coarse. Google returns 400 +
// status="INVALID_ARGUMENT" for invalid API keys, with the real
// classification only in details[].reason.
type errorResponse struct {
	Error struct {
		Code    int               `json:"code"`
		Message string            `json:"message"`
		Status  string            `json:"status"`
		Details []wireErrorDetail `json:"details,omitempty"`
	} `json:"error"`
}

// wireErrorDetail captures the subset of google.rpc.* detail messages the
// adapter needs to classify errors. Unknown @type entries are ignored.
type wireErrorDetail struct {
	Type   string `json:"@type"`
	Reason string `json:"reason"`
}

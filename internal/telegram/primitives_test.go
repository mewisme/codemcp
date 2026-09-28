package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/config"
)

func TestRichPresentationFallbackPreservesRepresentativeSemantics(t *testing.T) {
	tests := []struct {
		name   string
		blocks []RichBlock
		want   []string
	}{
		{"status", []RichBlock{{Kind: RichHeading, Title: "Status", Text: "Runtime overview"}, {Kind: RichTable, Rows: [][]string{{"runtime", "enabled"}, {"Telegram", "healthy"}}}}, []string{"Status", "Runtime overview", "runtime", "enabled", "Telegram", "healthy"}},
		{"doctor", []RichBlock{{Kind: RichSection, Title: "Doctor", Text: "Diagnostics"}, {Kind: RichList, Items: []string{"config valid", "runtime reachable"}}}, []string{"Doctor", "Diagnostics", "config valid", "runtime reachable"}},
		{"approval", []RichBlock{{Kind: RichQuote, Text: "Approve guarded request"}, {Kind: RichCode, Text: "cm status"}}, []string{"Approve guarded request", "cm status"}},
		{"resource", []RichBlock{{Kind: RichDetails, Title: "Workspace", Text: "ws_123"}, {Kind: RichLink, Title: "Docs", LinkURL: "https://example.com/docs"}}, []string{"Workspace", "ws_123", "Docs", "https://example.com/docs"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rich := BuildRichPresentation(test.blocks...)
			fallback := RichFallback(rich)
			for _, value := range test.want {
				if !strings.Contains(fallback.Text, value) && !strings.Contains(string(fallback.HTML), value) {
					t.Fatalf("fallback missing %q: text=%q html=%q", value, fallback.Text, fallback.HTML)
				}
			}
			if got := screenText(Screen{Rich: rich}); got != string(fallback.HTML) {
				t.Fatalf("rich screen fallback=%q want=%q", got, fallback.HTML)
			}
		})
	}
}

func TestRichMessageHTMLUsesNativeBlockStructureInsteadOfWhitespaceLayout(t *testing.T) {
	rich := BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Doctor", Text: "healthy=false · warnings=2"},
		RichBlock{Kind: RichList, Items: []string{"approval.lifecycle — healthy", "background.delivery — healthy"}},
		RichBlock{Kind: RichTable, Rows: [][]string{{"Runtime", "ready"}, {"Version", "v1.2.3"}}},
		RichBlock{Kind: RichDetails, Title: "Detail", Text: "line one\nline two"},
		RichBlock{Kind: RichQuote, Text: "quoted\ntext"},
		RichBlock{Kind: RichCode, Text: "cm status"},
	)
	html := string(RichMessageHTML(rich))
	for _, want := range []string{
		"<h2>Doctor</h2><p>healthy=false · warnings=2</p>",
		"<ul><li>approval.lifecycle — healthy</li><li>background.delivery — healthy</li></ul>",
		"<table compact><tr><th>Runtime</th><td>ready</td></tr><tr><th>Version</th><td>v1.2.3</td></tr></table>",
		"<details open><summary>Detail</summary><p>line one<br>line two</p></details>",
		"<blockquote>quoted<br>text</blockquote>",
		"<pre><code>cm status</code></pre>",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("native rich message missing block markup %q: %q", want, html)
		}
	}
	if strings.Contains(html, "<b>approval.lifecycle — healthy</b>\n") {
		t.Fatalf("native rich list regressed to newline-separated inline HTML: %q", html)
	}
}

func TestDoctorLikeRichMessageKeepsEachDiagnosticAsAListItem(t *testing.T) {
	items := []string{
		"approval.lifecycle — healthy · approval lifecycle is readable",
		"background.delivery — healthy · background delivery lifecycle is readable",
		"checkpoint.history — healthy · checkpoint history is readable",
	}
	html := string(RichMessageHTML(BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Doctor", Text: "healthy=false · warnings=2 · errors=0 · provider failures=0"},
		RichBlock{Kind: RichList, Items: items},
	)))
	if got := strings.Count(html, "<li>"); got != len(items) {
		t.Fatalf("doctor diagnostics list items=%d want=%d: %q", got, len(items), html)
	}
	if !strings.HasPrefix(html, "<h2>Doctor</h2><p>") || !strings.Contains(html, "</p>\n<ul>") {
		t.Fatalf("doctor heading/list are not separate native blocks: %q", html)
	}
}

func TestRichPresentationBoundsAndRejectsUnsafeLinks(t *testing.T) {
	blocks := make([]RichBlock, richMaxBlocks+5)
	for i := range blocks {
		blocks[i] = RichBlock{Kind: RichList, Items: make([]string, richMaxRows+5)}
	}
	rich := BuildRichPresentation(blocks...)
	if len(rich.Blocks) != richMaxBlocks || len(rich.Blocks[0].Items) != richMaxRows {
		t.Fatalf("rich bounds blocks=%d items=%d", len(rich.Blocks), len(rich.Blocks[0].Items))
	}
	unsafe := BuildRichPresentation(RichBlock{Kind: RichLink, Title: "unsafe", LinkURL: "javascript:alert(1)"})
	if unsafe.Blocks[0].LinkURL != "" {
		t.Fatalf("unsafe link retained: %#v", unsafe.Blocks[0])
	}
}

func TestRichCopyUsesNativeInlineCopyAndCodeFallback(t *testing.T) {
	rich := BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Approval", Text: "Pending"},
		RichBlock{Kind: RichCopy, Title: "ID", Text: "req_123", CopyText: "req_123"},
	)
	native := string(RichMessageHTML(rich))
	for _, want := range []string{"<tg-button type=\"copy_text\"", "text=\"req_123\"", ">req_123</tg-button>"} {
		if !strings.Contains(native, want) {
			t.Fatalf("native rich copy missing %q: %q", want, native)
		}
	}
	fallback := RichFallback(rich)
	if !strings.Contains(string(fallback.HTML), "<code>req_123</code>") || strings.Contains(string(fallback.HTML), "tg-button") {
		t.Fatalf("fallback copy rendering=%q", fallback.HTML)
	}
	input, ok := screenRichMessage(Screen{Rich: rich})
	if !ok || !strings.Contains(input.HTML, "type=\"copy_text\"") {
		t.Fatalf("native input rich message=%#v ok=%v", input, ok)
	}
}

func TestRichCopyRejectsOversizedCopyAuthorityButKeepsSafeFallback(t *testing.T) {
	value := strings.Repeat("界", MaxCopyTextRunes+1)
	rich := BuildRichPresentation(RichBlock{Kind: RichCopy, Title: "ID", Text: value, CopyText: value})
	if rich.Blocks[0].CopyText != "" {
		t.Fatalf("oversized rich copy retained action: %#v", rich.Blocks[0])
	}
	native := string(RichMessageHTML(rich))
	if strings.Contains(native, "tg-button") {
		t.Fatalf("oversized rich copy retained native button: %q", native)
	}
	if !strings.Contains(string(RichFallback(rich).HTML), "<code>") {
		t.Fatalf("oversized copy lost fallback presentation: %q", RichFallback(rich).HTML)
	}
}

func TestPendingInputRequiresExactReplyOwnerAndGeneration(t *testing.T) {
	store := NewInputStore(time.Minute)
	owner := ViewOwner{ChatID: 42, UserID: 7, Generation: 3}
	message := func(chat, user, prompt int64) Message {
		return Message{MessageID: 99, Chat: Chat{ID: chat, Type: "private"}, From: &User{ID: user}, Text: "value", ReplyToMessage: &Message{MessageID: prompt}}
	}
	for _, test := range []struct {
		name       string
		matchOwner ViewOwner
		message    Message
		want       bool
	}{
		{"unrelated", owner, Message{Chat: Chat{ID: 42}, From: &User{ID: 7}, Text: "value"}, false},
		{"wrong-user", owner, message(42, 8, 10), false},
		{"wrong-chat", owner, message(43, 7, 10), false},
		{"stale-generation", ViewOwner{ChatID: 42, UserID: 7, Generation: 4}, message(42, 7, 10), false},
		{"exact", owner, message(42, 7, 10), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store = NewInputStore(time.Minute)
			if err := store.Put(owner, 10, true); err != nil {
				t.Fatal(err)
			}
			_, got := store.Match(test.matchOwner, test.message)
			if got != test.want {
				t.Fatalf("match=%v want=%v", got, test.want)
			}
		})
	}
	store = NewInputStore(time.Nanosecond)
	if err := store.Put(owner, 11, false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, ok := store.Match(owner, message(42, 7, 11)); ok {
		t.Fatal("expired input state matched")
	}
}

func TestDocumentValidationRejectsTraversalAndOversizeBeforeTransport(t *testing.T) {
	if err := ValidateDocument(Document{FileID: "file", FileName: "../secret", FileSize: 1}); err == nil {
		t.Fatal("path traversal filename accepted")
	}
	if err := ValidateDocument(Document{FileID: "file", FileName: "safe.json", FileSize: MaxFileTransferBytes + 1}); err == nil {
		t.Fatal("oversized document accepted")
	}
	if err := ValidateDocumentUpload(DocumentUpload{FileName: "../secret", Data: []byte("x")}); err == nil {
		t.Fatal("unsafe upload filename accepted")
	}
	if err := ValidateDocumentUpload(DocumentUpload{FileName: "safe.json", Data: make([]byte, MaxFileTransferBytes+1)}); err == nil {
		t.Fatal("oversized upload accepted")
	}
}

func TestBotAPIRichMessageUsesForceReplyAndProtectContent(t *testing.T) {
	type captured struct {
		Protect     bool
		Force       bool
		Placeholder string
		Selective   bool
	}
	requests := make(chan captured, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(strings.ToLower(r.URL.Path), "/sendmessage") {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		var markup struct {
			ForceReply            bool   `json:"force_reply"`
			InputFieldPlaceholder string `json:"input_field_placeholder"`
			Selective             bool   `json:"selective"`
		}
		if err := json.Unmarshal([]byte(r.FormValue("reply_markup")), &markup); err != nil {
			t.Fatalf("decode reply markup: %v", err)
		}
		requests <- captured{Protect: r.FormValue("protect_content") == "true", Force: markup.ForceReply, Placeholder: markup.InputFieldPlaceholder, Selective: markup.Selective}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 17, "chat": map[string]any{"id": 42, "type": "private"}, "date": 1}})
	}))
	defer server.Close()
	client := newAPIClientWithOptions("123456:test-token", time.Second, server.URL, server.Client())
	id, err := client.SendRichMessage(t.Context(), 42, Screen{Text: "secret input"}, RichMessageOptions{ForceReplyPlaceholder: strings.Repeat("x", maxForceReplyPlaceholderRunes+20), ProtectContent: true})
	if err != nil {
		t.Fatal(err)
	}
	if id != 17 {
		t.Fatalf("message id=%d", id)
	}
	got := <-requests
	if !got.Protect || !got.Force || !got.Selective || len([]rune(got.Placeholder)) != maxForceReplyPlaceholderRunes {
		t.Fatalf("rich request=%#v", got)
	}
}

type primitiveAPI struct {
	interactiveTestAPI
	nextID  int64
	options RichMessageOptions
}

func (api *primitiveAPI) SendRichMessage(_ context.Context, _ int64, _ Screen, options RichMessageOptions) (int64, error) {
	api.options = options
	return api.nextID, nil
}

func (*primitiveAPI) SendChatAction(context.Context, int64, string) error { return nil }

func TestPromptInputBindsRuntimeGenerationAndSecretPolicy(t *testing.T) {
	api := &primitiveAPI{nextID: 55}
	runtime := &Runtime{
		api: api, generation: 9,
		config: config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}},
		health: Health{Running: true, Enabled: true, AuthorizationConfigured: true},
	}
	ui, err := NewInterface(InterfaceOptions{Runtime: runtime})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 9}
	id, err := ui.PromptInput(t.Context(), owner, Screen{Text: "token"}, "Enter token", true)
	if err != nil {
		t.Fatal(err)
	}
	if id != 55 || !api.options.ProtectContent {
		t.Fatalf("prompt id=%d options=%#v", id, api.options)
	}
	input := Message{MessageID: 56, Chat: Chat{ID: 42, Type: "private"}, From: &User{ID: 42}, Text: "value", ReplyToMessage: &Message{MessageID: 55}}
	state, ok := ui.acceptPendingInput(Update{Message: &input})
	if !ok || state.Owner != owner || !state.Secret {
		t.Fatalf("accepted state=%#v ok=%v", state, ok)
	}
	if _, ok := ui.acceptPendingInput(Update{Message: &input}); ok {
		t.Fatal("pending input was reusable")
	}
}

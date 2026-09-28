package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/notification"
)

type topicTestAPI struct {
	mu sync.Mutex

	nextThreadID      int
	created           []string
	threadSends       []string
	richThreadScreens []Screen
	richThreadIDs     []int
	editedMessageIDs  []int64
	editedScreens     []Screen
	generalSends      []string
	threadErr         error
	editErr           error
}

func (api *topicTestAPI) GetMe(context.Context) (User, error) {
	return User{ID: 1, HasTopicsEnabled: true, AllowsUsersToCreateTopics: true}, nil
}

func (api *topicTestAPI) SendMessage(_ context.Context, chatID int64, text string) error {
	api.mu.Lock()
	api.generalSends = append(api.generalSends, fmt.Sprintf("%d:%s", chatID, text))
	api.mu.Unlock()
	return nil
}

func (api *topicTestAPI) CreateTopic(_ context.Context, chatID int64, name string) (int, error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.nextThreadID++
	api.created = append(api.created, fmt.Sprintf("%d:%s", chatID, name))
	return api.nextThreadID, nil
}

func (api *topicTestAPI) SendMessageThread(_ context.Context, chatID int64, threadID int, text string) error {
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.threadErr != nil {
		return api.threadErr
	}
	api.threadSends = append(api.threadSends, fmt.Sprintf("%d:%d:%s", chatID, threadID, text))
	return nil
}

func (api *topicTestAPI) SendRichMessageThread(_ context.Context, _ int64, threadID int, screen Screen, _ RichMessageOptions) (int64, error) {
	api.mu.Lock()
	api.richThreadIDs = append(api.richThreadIDs, threadID)
	api.richThreadScreens = append(api.richThreadScreens, screen)
	api.mu.Unlock()
	return 1, nil
}

func (*topicTestAPI) SendChatActionThread(context.Context, int64, int, string) error { return nil }

func (*topicTestAPI) SendDocumentThread(context.Context, int64, int, DocumentUpload) error {
	return nil
}

func (*topicTestAPI) SendScreen(context.Context, int64, Screen) error { return nil }

func (api *topicTestAPI) EditScreen(_ context.Context, _ int64, messageID int64, screen Screen) error {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.editedMessageIDs = append(api.editedMessageIDs, messageID)
	api.editedScreens = append(api.editedScreens, screen)
	return api.editErr
}

func (*topicTestAPI) AnswerCallback(context.Context, string, string, bool) error { return nil }

func TestTopicRoleForNotification(t *testing.T) {
	tests := map[notification.Kind]TopicRole{
		notification.KindApprovalPending:       TopicRequests,
		notification.KindApprovalResolved:      TopicRequests,
		notification.KindCompletionAccepted:    TopicCompletions,
		notification.KindBackgroundJobFinished: TopicRuntime,
		notification.Kind("log.status"):        TopicLogs,
		notification.Kind("runtime.update"):    TopicRuntime,
		notification.Kind("doctor.warning"):    TopicRuntime,
	}
	for kind, want := range tests {
		if got := topicRoleForNotification(kind); got != want {
			t.Fatalf("kind=%q role=%q want=%q", kind, got, want)
		}
	}
}

func TestTopicReconcileCreatesStableManagedTopicsOnce(t *testing.T) {
	api := &topicTestAPI{nextThreadID: 100}
	runtime := &Runtime{
		root:   t.TempDir(),
		api:    api,
		topics: newTopicStore(t.TempDir()),
	}
	cfg := config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, TopicsEnabled: true}

	runtime.reconcileTopicsBounded(t.Context(), api, cfg, true)
	if got := len(api.created); got != len(managedTopicRoles) {
		t.Fatalf("created topics=%d want=%d (%v)", got, len(managedTopicRoles), api.created)
	}
	if health := runtime.Health(); !health.TopicsConfigured || !health.TopicsSupported || !health.TopicsEffective || health.TopicCount != len(managedTopicRoles) || health.TopicLastError != "" {
		t.Fatalf("topic health=%#v", health)
	}
	for _, managed := range managedTopicRoles {
		if runtime.topics.get(42, managed.Role) <= 0 {
			t.Fatalf("missing persisted topic role %q", managed.Role)
		}
	}

	runtime.reconcileTopicsBounded(t.Context(), api, cfg, true)
	if got := len(api.created); got != len(managedTopicRoles) {
		t.Fatalf("reconcile duplicated topics: %v", api.created)
	}

	reloaded := newTopicStore(filepathDir(runtime.topics.path))
	for _, managed := range managedTopicRoles {
		if got := reloaded.get(42, managed.Role); got != runtime.topics.get(42, managed.Role) {
			t.Fatalf("reloaded role=%q thread=%d want=%d", managed.Role, got, runtime.topics.get(42, managed.Role))
		}
	}
}

func filepathDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}

func TestNotificationRoutingUsesManagedTopicWithoutChangingDeliveryCount(t *testing.T) {
	api := &topicTestAPI{}
	store := newTopicStore(t.TempDir())
	roles := []TopicRole{TopicRequests, TopicCompletions, TopicRuntime, TopicLogs}
	for index, role := range roles {
		if err := store.put(42, role, 100+index); err != nil {
			t.Fatal(err)
		}
	}
	runtime := &Runtime{
		api:    api,
		topics: store,
		config: config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, TopicsEnabled: true},
		health: Health{Running: true, Enabled: true, AuthorizationConfigured: true, TopicsConfigured: true, TopicsSupported: true, TopicsEffective: true},
	}
	messages := []notification.Message{
		{Kind: notification.KindApprovalPending, Title: "approval"},
		{Kind: notification.KindCompletionAccepted, Title: "completion"},
		{Kind: notification.Kind("runtime.update"), Title: "runtime"},
		{Kind: notification.Kind("log.status"), Title: "logs"},
	}
	for _, message := range messages {
		if err := runtime.SendNotification(t.Context(), message); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(api.threadSends); got != len(messages) {
		t.Fatalf("thread deliveries=%d want=%d (%v)", got, len(messages), api.threadSends)
	}
	if len(api.generalSends) != 0 {
		t.Fatalf("unexpected General deliveries: %v", api.generalSends)
	}
	for index, wantThread := range []string{"42:100:", "42:101:", "42:102:", "42:103:"} {
		if got := api.threadSends[index]; len(got) < len(wantThread) || got[:len(wantThread)] != wantThread {
			t.Fatalf("delivery[%d]=%q want prefix %q", index, got, wantThread)
		}
	}
}

func TestInteractiveApprovalNotificationUsesRichRequestsTopicDelivery(t *testing.T) {
	api := &topicTestAPI{}
	store := newTopicStore(t.TempDir())
	if err := store.put(42, TopicRequests, 120); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{
		api: api, topics: store,
		config: config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, TopicsEnabled: true},
		health: Health{Running: true, Enabled: true, AuthorizationConfigured: true, TopicsEffective: true},
		notificationRenderer: func(_ context.Context, chatID int64, message notification.Message) (Screen, bool, error) {
			if chatID != 42 || message.Kind != notification.KindApprovalPending || message.RequestID != "req_1" {
				t.Fatalf("renderer chat=%d message=%#v", chatID, message)
			}
			return Screen{
				Rich:     BuildRichPresentation(RichBlock{Kind: RichHeading, Title: "Approval requested", Text: message.RequestID}),
				Keyboard: [][]Button{{{Text: "Approve once", CallbackData: "opaque", Role: ButtonRolePositive}}},
			}, true, nil
		},
	}
	if err := runtime.SendNotification(t.Context(), notification.Message{
		Kind: notification.KindApprovalPending, RequestID: "req_1", Title: "Approval requested",
	}); err != nil {
		t.Fatal(err)
	}
	if len(api.richThreadScreens) != 1 || len(api.richThreadIDs) != 1 || api.richThreadIDs[0] != 120 {
		t.Fatalf("rich topic deliveries ids=%v screens=%d", api.richThreadIDs, len(api.richThreadScreens))
	}
	if len(api.threadSends) != 0 || len(api.generalSends) != 0 {
		t.Fatalf("interactive notification duplicated as text thread=%v general=%v", api.threadSends, api.generalSends)
	}
	if len(api.richThreadScreens[0].Keyboard) == 0 || api.richThreadScreens[0].Keyboard[0][0].Text != "Approve once" {
		t.Fatalf("interactive notification keyboard=%#v", api.richThreadScreens[0].Keyboard)
	}
}

func TestApprovalResolvedNotificationEditsOriginalRichCardAfterRuntimeRestart(t *testing.T) {
	root := t.TempDir()
	api := &topicTestAPI{}
	store := newTopicStore(root)
	if err := store.put(42, TopicRequests, 120); err != nil {
		t.Fatal(err)
	}
	renderer := func(_ context.Context, chatID int64, message notification.Message) (Screen, bool, error) {
		if chatID != 42 || message.RequestID != "req_1" {
			t.Fatalf("renderer chat=%d message=%#v", chatID, message)
		}
		return Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: string(message.Kind), Text: message.RequestID})}, true, nil
	}
	pendingRuntime := &Runtime{
		root: root, api: api, topics: store,
		config:               config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, TopicsEnabled: true},
		health:               Health{Running: true, Enabled: true, AuthorizationConfigured: true, TopicsEffective: true},
		notificationRenderer: renderer,
	}
	if err := pendingRuntime.SendNotification(t.Context(), notification.Message{
		Kind: notification.KindApprovalPending, RequestID: "req_1", Title: "Approval requested",
	}); err != nil {
		t.Fatal(err)
	}
	if len(api.richThreadScreens) != 1 || len(api.generalSends) != 0 || len(api.threadSends) != 0 {
		t.Fatalf("pending delivery rich=%d general=%v thread=%v", len(api.richThreadScreens), api.generalSends, api.threadSends)
	}
	if got := pendingRuntime.approvalMessages.get(42, "req_1"); got != 1 {
		t.Fatalf("stored approval message id=%d want=1", got)
	}

	resolvedRuntime := &Runtime{
		root: root, api: api, topics: newTopicStore(root),
		config:               config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, TopicsEnabled: true},
		health:               Health{Running: true, Enabled: true, AuthorizationConfigured: true, TopicsEffective: true},
		notificationRenderer: renderer,
	}
	if err := resolvedRuntime.SendNotification(t.Context(), notification.Message{
		Kind: notification.KindApprovalResolved, RequestID: "req_1", Title: "Approval resolved",
	}); err != nil {
		t.Fatal(err)
	}
	if len(api.richThreadScreens) != 1 {
		t.Fatalf("resolved notification created another rich message: %d", len(api.richThreadScreens))
	}
	if len(api.generalSends) != 0 || len(api.threadSends) != 0 {
		t.Fatalf("resolved notification emitted redundant text general=%v thread=%v", api.generalSends, api.threadSends)
	}
	if len(api.editedMessageIDs) != 1 || api.editedMessageIDs[0] != 1 {
		t.Fatalf("resolved edits=%v want=[1]", api.editedMessageIDs)
	}
	if resolvedRuntime.approvalMessages == nil || resolvedRuntime.approvalMessages.get(42, "req_1") != 0 {
		t.Fatal("resolved approval message reference was not cleared")
	}
	fallback := RichFallback(api.editedScreens[0].Rich).Text
	if !strings.Contains(fallback, string(notification.KindApprovalResolved)) {
		t.Fatalf("resolved edit screen=%q", fallback)
	}
}

func TestApprovalResolvedBadRequestDoesNotAppendDuplicateMessage(t *testing.T) {
	root := t.TempDir()
	api := &topicTestAPI{editErr: &transportError{Class: transportErrorBadRequest}}
	store := newApprovalMessageStore(root)
	if err := store.put(42, "req_1", 9); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{
		root: root, api: api, approvalMessages: store,
		config: config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}},
		health: Health{Running: true, Enabled: true, AuthorizationConfigured: true},
		notificationRenderer: func(_ context.Context, _ int64, _ notification.Message) (Screen, bool, error) {
			return Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: "approved"})}, true, nil
		},
	}
	if err := runtime.SendNotification(t.Context(), notification.Message{
		Kind: notification.KindApprovalResolved, RequestID: "req_1",
	}); err != nil {
		t.Fatal(err)
	}
	if len(api.editedMessageIDs) != 1 || api.editedMessageIDs[0] != 9 {
		t.Fatalf("resolved edits=%v want=[9]", api.editedMessageIDs)
	}
	if len(api.richThreadScreens) != 0 || len(api.generalSends) != 0 || len(api.threadSends) != 0 {
		t.Fatalf("bad-request edit appended duplicate rich=%d general=%v thread=%v", len(api.richThreadScreens), api.generalSends, api.threadSends)
	}
	if store.get(42, "req_1") != 0 {
		t.Fatal("failed resolved edit retained stale approval message reference")
	}
}

func TestMissingManagedTopicFallsBackToGeneralAndInvalidatesMetadata(t *testing.T) {
	api := &topicTestAPI{threadErr: &transportError{Class: transportErrorBadRequest}}
	store := newTopicStore(t.TempDir())
	if err := store.put(42, TopicRequests, 77); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{
		api:    api,
		topics: store,
		config: config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, TopicsEnabled: true},
		health: Health{Running: true, Enabled: true, AuthorizationConfigured: true, TopicsConfigured: true, TopicsSupported: true, TopicsEffective: true},
	}
	if err := runtime.SendNotification(t.Context(), notification.Message{Kind: notification.KindApprovalPending, Title: "approval"}); err != nil {
		t.Fatal(err)
	}
	if len(api.generalSends) != 1 {
		t.Fatalf("General fallback deliveries=%v", api.generalSends)
	}
	if got := store.get(42, TopicRequests); got != 0 {
		t.Fatalf("stale topic metadata retained thread=%d", got)
	}
	if runtime.Health().TopicLastError == "" {
		t.Fatal("missing topic was not surfaced in diagnostics")
	}
}

func TestAmbiguousTopicTransportFailureDoesNotDuplicateInGeneral(t *testing.T) {
	api := &topicTestAPI{threadErr: errors.New("network down")}
	store := newTopicStore(t.TempDir())
	if err := store.put(42, TopicRequests, 77); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{
		api: api, topics: store,
		config: config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, TopicsEnabled: true},
		health: Health{Running: true, Enabled: true, AuthorizationConfigured: true, TopicsEffective: true},
	}
	if err := runtime.SendNotification(t.Context(), notification.Message{Kind: notification.KindApprovalPending, Title: "approval"}); err == nil {
		t.Fatal("ambiguous topic transport failure was hidden")
	}
	if len(api.generalSends) != 0 {
		t.Fatalf("ambiguous failure duplicated into General: %v", api.generalSends)
	}
	if got := store.get(42, TopicRequests); got != 77 {
		t.Fatalf("ambiguous failure invalidated topic metadata: %d", got)
	}
}

func TestUnsupportedTopicCapabilityKeepsGeneralBaseline(t *testing.T) {
	api := &topicTestAPI{}
	runtime := &Runtime{
		api: api, topics: newTopicStore(t.TempDir()),
		config: config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, TopicsEnabled: true},
		health: Health{Running: true, Enabled: true, AuthorizationConfigured: true},
	}
	runtime.reconcileTopicsBounded(t.Context(), api, runtime.config, false)
	health := runtime.Health()
	if !health.TopicsConfigured || health.TopicsSupported || health.TopicsEffective {
		t.Fatalf("unsupported topic health=%#v", health)
	}
	if len(api.created) != 0 {
		t.Fatalf("unsupported topic mode created topics: %v", api.created)
	}
	if err := runtime.SendNotification(t.Context(), notification.Message{Kind: notification.KindApprovalPending, Title: "approval"}); err != nil {
		t.Fatal(err)
	}
	if len(api.generalSends) != 1 || len(api.threadSends) != 0 {
		t.Fatalf("unsupported routing thread=%v general=%v", api.threadSends, api.generalSends)
	}
}

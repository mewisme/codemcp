package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/notification"
)

type topicTestAPI struct {
	mu sync.Mutex

	nextThreadID        int
	created             []string
	threadSends         []string
	richThreadScreens   []Screen
	richThreadIDs       []int
	richGeneralScreens  []Screen
	chatActionThreadIDs []int
	documentThreadIDs   []int
	editedMessageIDs    []int64
	editedScreens       []Screen
	deletedMessageIDs   []int64
	generalSends        []string
	threadErr           error
	editErr             error
	deleteErr           error
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
	defer api.mu.Unlock()
	if api.threadErr != nil {
		return 0, api.threadErr
	}
	api.richThreadIDs = append(api.richThreadIDs, threadID)
	api.richThreadScreens = append(api.richThreadScreens, screen)
	return 1, nil
}

func (api *topicTestAPI) SendRichMessage(_ context.Context, _ int64, screen Screen, _ RichMessageOptions) (int64, error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.richGeneralScreens = append(api.richGeneralScreens, screen)
	return 1, nil
}

func (*topicTestAPI) SendChatAction(context.Context, int64, string) error { return nil }

func (api *topicTestAPI) SendChatActionThread(_ context.Context, _ int64, threadID int, _ string) error {
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.threadErr != nil {
		return api.threadErr
	}
	api.chatActionThreadIDs = append(api.chatActionThreadIDs, threadID)
	return nil
}

func (api *topicTestAPI) SendDocumentThread(_ context.Context, _ int64, threadID int, _ DocumentUpload) error {
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.threadErr != nil {
		return api.threadErr
	}
	api.documentThreadIDs = append(api.documentThreadIDs, threadID)
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

func (api *topicTestAPI) DeleteMessage(_ context.Context, _ int64, messageID int64) error {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.deletedMessageIDs = append(api.deletedMessageIDs, messageID)
	return api.deleteErr
}

func TestTopicRoleForNotification(t *testing.T) {
	tests := map[notification.Kind]TopicRole{
		notification.KindApprovalPending:       TopicRequests,
		notification.KindApprovalResolved:      TopicRequests,
		notification.KindApprovalUpdated:       TopicRequests,
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

func TestBackgroundExplanationNotificationEditsOriginalRichCard(t *testing.T) {
	api := &topicTestAPI{}
	store := newTopicStore(t.TempDir())
	if err := store.put(42, TopicRuntime, 120); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{
		api: api, topics: store,
		health: Health{Running: true, Enabled: true, AuthorizationConfigured: true, TopicsEffective: true},
	}
	base := notification.Message{ID: "background:proc_1", Kind: notification.KindBackgroundJobFinished, ProcessID: "proc_1"}
	if err := runtime.handleRenderedNotification(t.Context(), 42, TopicRuntime, base, Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: "base"})}); err != nil {
		t.Fatal(err)
	}
	update := base
	update.Update = true
	if err := runtime.handleRenderedNotification(t.Context(), 42, TopicRuntime, update, Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: "explained"})}); err != nil {
		t.Fatal(err)
	}
	if len(api.richThreadScreens) != 1 {
		t.Fatalf("background update created another rich message: %d", len(api.richThreadScreens))
	}
	if len(api.editedMessageIDs) != 1 || api.editedMessageIDs[0] != 1 {
		t.Fatalf("background update edits=%v want=[1]", api.editedMessageIDs)
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

func TestTopicDeliveryPrimitivesUseManagedRoleThreads(t *testing.T) {
	api := &topicTestAPI{}
	store := newTopicStore(t.TempDir())
	roles := []TopicRole{TopicRequests, TopicCompletions, TopicRuntime, TopicLogs}
	for index, role := range roles {
		if err := store.put(42, role, 200+index); err != nil {
			t.Fatal(err)
		}
	}
	runtime := &Runtime{
		api: api, topics: store,
		health: Health{Running: true, TopicsEffective: true},
	}
	for _, role := range roles {
		if _, err := runtime.SendRichMessageToTopic(t.Context(), 42, role, Screen{Text: "message"}, RichMessageOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := runtime.SendChatActionToTopic(t.Context(), 42, role, "typing"); err != nil {
			t.Fatal(err)
		}
		if err := runtime.SendDocumentToTopic(t.Context(), 42, role, DocumentUpload{FileName: "artifact.txt", Data: []byte("x")}); err != nil {
			t.Fatal(err)
		}
	}
	want := []int{200, 201, 202, 203}
	if fmt.Sprint(api.richThreadIDs) != fmt.Sprint(want) {
		t.Fatalf("rich threads=%v want=%v", api.richThreadIDs, want)
	}
	if fmt.Sprint(api.chatActionThreadIDs) != fmt.Sprint(want) {
		t.Fatalf("chat action threads=%v want=%v", api.chatActionThreadIDs, want)
	}
	if fmt.Sprint(api.documentThreadIDs) != fmt.Sprint(want) {
		t.Fatalf("document threads=%v want=%v", api.documentThreadIDs, want)
	}
}

func TestTopicReconcilePrunesRemovedAllowlistUsersOnly(t *testing.T) {
	api := &topicTestAPI{nextThreadID: 500}
	root := t.TempDir()
	store := newTopicStore(root)
	for _, userID := range []int64{42, 43} {
		for index, role := range []TopicRole{TopicRequests, TopicCompletions, TopicRuntime, TopicLogs} {
			if err := store.put(userID, role, int(userID)*10+index); err != nil {
				t.Fatal(err)
			}
		}
	}
	runtime := &Runtime{api: api, topics: store}
	cfg := config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, TopicsEnabled: true}
	runtime.reconcileTopicsBounded(t.Context(), api, cfg, true)
	for _, role := range []TopicRole{TopicRequests, TopicCompletions, TopicRuntime, TopicLogs} {
		if store.get(42, role) == 0 {
			t.Fatalf("authorized user's %q mapping was pruned", role)
		}
		if store.get(43, role) != 0 {
			t.Fatalf("removed user's %q mapping survived allowlist reconcile", role)
		}
	}
	if len(api.created) != 0 || runtime.Health().TopicCount != 4 {
		t.Fatalf("reconcile created=%v health=%#v", api.created, runtime.Health())
	}
	reloaded := newTopicStore(root)
	for _, role := range []TopicRole{TopicRequests, TopicCompletions, TopicRuntime, TopicLogs} {
		if reloaded.get(42, role) == 0 || reloaded.get(43, role) != 0 {
			t.Fatalf("persisted prune drift role=%q authorized=%d removed=%d", role, reloaded.get(42, role), reloaded.get(43, role))
		}
	}
}

func TestTopicDisableReenableRetainsOnlyAuthorizedMappings(t *testing.T) {
	api := &topicTestAPI{}
	store := newTopicStore(t.TempDir())
	for index, role := range []TopicRole{TopicRequests, TopicCompletions, TopicRuntime, TopicLogs} {
		if err := store.put(42, role, 300+index); err != nil {
			t.Fatal(err)
		}
	}
	runtime := &Runtime{
		api: api, topics: store,
		config: config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, TopicsEnabled: true},
		health: Health{Enabled: true, Running: true, PollingHealthy: true, AuthorizationConfigured: true},
	}
	disabled := runtime.config
	disabled.TopicsEnabled = false
	runtime.reconcileTopicsBounded(t.Context(), api, disabled, true)
	if runtime.Health().TopicsEffective {
		t.Fatal("disabled topics remained effective")
	}
	if err := runtime.SendNotification(t.Context(), notification.Message{Kind: notification.KindApprovalPending, Title: "general"}); err != nil {
		t.Fatal(err)
	}
	if len(api.generalSends) != 1 || len(api.threadSends) != 0 {
		t.Fatalf("disabled routing thread=%v general=%v", api.threadSends, api.generalSends)
	}
	runtime.reconcileTopicsBounded(t.Context(), api, runtime.config, true)
	if !runtime.Health().TopicsEffective || len(api.created) != 0 || store.get(42, TopicRequests) != 300 {
		t.Fatalf("re-enabled state created=%v health=%#v requests=%d", api.created, runtime.Health(), store.get(42, TopicRequests))
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

func TestApprovalResolvedNotificationReplacesOriginalRichCardAfterRuntimeRestart(t *testing.T) {
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

	updatedRuntime := &Runtime{
		root: root, api: api, topics: newTopicStore(root),
		config:               config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, TopicsEnabled: true},
		health:               Health{Running: true, Enabled: true, AuthorizationConfigured: true, TopicsEffective: true},
		notificationRenderer: renderer,
	}
	if err := updatedRuntime.SendNotification(t.Context(), notification.Message{
		Kind: notification.KindApprovalUpdated, RequestID: "req_1", Title: "Approval explanation ready",
	}); err != nil {
		t.Fatal(err)
	}
	if len(api.richThreadScreens) != 1 || len(api.editedMessageIDs) != 1 || api.editedMessageIDs[0] != 1 {
		t.Fatalf("approval update sends=%d edits=%v", len(api.richThreadScreens), api.editedMessageIDs)
	}
	if updatedRuntime.approvalMessages == nil || updatedRuntime.approvalMessages.get(42, "req_1") != 1 {
		t.Fatal("approval update did not retain pending message reference")
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
	if len(api.richThreadScreens) != 2 {
		t.Fatalf("resolved notification fresh rich messages=%d want=2 total including pending", len(api.richThreadScreens))
	}
	if len(api.generalSends) != 0 || len(api.threadSends) != 0 {
		t.Fatalf("resolved notification emitted redundant text general=%v thread=%v", api.generalSends, api.threadSends)
	}
	if len(api.editedMessageIDs) != 1 || api.editedMessageIDs[0] != 1 {
		t.Fatalf("resolved edits=%v want only non-terminal update on message 1", api.editedMessageIDs)
	}
	if len(api.deletedMessageIDs) != 1 || api.deletedMessageIDs[0] != 1 {
		t.Fatalf("resolved deletes=%v want=[1]", api.deletedMessageIDs)
	}
	if resolvedRuntime.approvalMessages == nil || resolvedRuntime.approvalMessages.get(42, "req_1") != 0 {
		t.Fatal("resolved approval message reference was not cleared")
	}
	fallback := RichFallback(api.richThreadScreens[1].Rich).Text
	if !strings.Contains(fallback, string(notification.KindApprovalResolved)) {
		t.Fatalf("resolved fresh screen=%q", fallback)
	}
}

func TestApprovalResolvedMissingOldCardStillSendsOneFreshMessage(t *testing.T) {
	root := t.TempDir()
	api := &topicTestAPI{deleteErr: &transportError{Class: transportErrorNotFound}}
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
	if len(api.deletedMessageIDs) != 1 || api.deletedMessageIDs[0] != 9 {
		t.Fatalf("resolved deletes=%v want=[9]", api.deletedMessageIDs)
	}
	if len(api.richThreadScreens) != 0 || len(api.richGeneralScreens) != 1 || len(api.generalSends) != 0 || len(api.threadSends) != 0 {
		t.Fatalf("missing old card fresh delivery topic-rich=%d general-rich=%d general=%v thread=%v", len(api.richThreadScreens), len(api.richGeneralScreens), api.generalSends, api.threadSends)
	}
	if store.get(42, "req_1") != 0 {
		t.Fatal("missing old card retained stale approval message reference")
	}
}

func TestApprovalResolvedDeleteFailureDoesNotSendFreshMessage(t *testing.T) {
	root := t.TempDir()
	api := &topicTestAPI{deleteErr: &transportError{Class: transportErrorForbidden}}
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
	}); err == nil {
		t.Fatal("resolved delete failure unexpectedly succeeded")
	}
	if len(api.deletedMessageIDs) != 1 || api.deletedMessageIDs[0] != 9 {
		t.Fatalf("resolved deletes=%v want=[9]", api.deletedMessageIDs)
	}
	if len(api.richThreadScreens) != 0 || len(api.richGeneralScreens) != 0 || len(api.generalSends) != 0 || len(api.threadSends) != 0 {
		t.Fatalf("delete failure emitted fresh message topic-rich=%d general-rich=%d general=%v thread=%v", len(api.richThreadScreens), len(api.richGeneralScreens), api.generalSends, api.threadSends)
	}
	if store.get(42, "req_1") != 9 {
		t.Fatal("delete failure cleared retryable approval message reference")
	}
}

func TestApprovalCallbackAndResolvedNotificationOrderingNeverDuplicatesFreshCard(t *testing.T) {
	for _, test := range []struct {
		name              string
		notificationFirst bool
	}{
		{name: "callback-before-notification"},
		{name: "notification-before-callback", notificationFirst: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			api := &topicTestAPI{}
			topics := newTopicStore(root)
			if err := topics.put(42, TopicRequests, 120); err != nil {
				t.Fatal(err)
			}
			runtime := &Runtime{
				root: root, api: api, topics: topics, generation: 7,
				config: config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, TopicsEnabled: true},
				health: Health{Running: true, Enabled: true, AuthorizationConfigured: true, TopicsEffective: true},
				notificationRenderer: func(_ context.Context, _ int64, message notification.Message) (Screen, bool, error) {
					return Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: string(message.Kind)})}, true, nil
				},
			}
			if err := runtime.SendNotification(t.Context(), notification.Message{
				Kind: notification.KindApprovalPending, RequestID: "req_race",
			}); err != nil {
				t.Fatal(err)
			}
			if runtime.approvalMessages == nil || runtime.approvalMessages.get(42, "req_race") != 1 {
				t.Fatal("pending approval reference was not persisted")
			}

			ui, err := NewInterface(InterfaceOptions{
				Runtime:    runtime,
				Dispatcher: &recordingDispatcher{result: approval.Request{ID: "req_race", Status: approval.StatusApproved}},
			})
			if err != nil {
				t.Fatal(err)
			}
			owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 7}
			button, err := ui.stateButton(owner, "Approve once", CallbackOpen, ActionState{
				Route: RouteOperation, Back: RouteRequests, Operation: capability.RequestApprove, ResourceID: "req_race",
				Input: application.RequestResolutionInput{ID: "req_race"},
			})
			if err != nil {
				t.Fatal(err)
			}
			callback := func() {
				ui.handleCallback(t.Context(), Update{CallbackQuery: &CallbackQuery{
					ID: "cb_race", From: User{ID: 42}, Data: button.CallbackData,
					Message: &Message{MessageID: 1, Chat: Chat{ID: 42, Type: "private"}},
				}})
			}
			resolved := func() {
				if err := runtime.SendNotification(t.Context(), notification.Message{
					Kind: notification.KindApprovalResolved, RequestID: "req_race",
				}); err != nil {
					t.Fatal(err)
				}
			}
			if test.notificationFirst {
				resolved()
				callback()
			} else {
				callback()
				resolved()
			}

			if got := len(api.richThreadScreens); got != 2 {
				t.Fatalf("fresh rich deliveries=%d want pending+one resolved", got)
			}
			if got := runtime.approvalMessages.get(42, "req_race"); got != 0 {
				t.Fatalf("terminal approval reference=%d want=0", got)
			}
			if len(api.editedMessageIDs) != 0 {
				t.Fatalf("terminal race edited pending card: %v", api.editedMessageIDs)
			}
		})
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
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		health := runtime.Health()
		if store.get(42, TopicRequests) != 0 && !health.TopicReconcilePending && health.TopicCount == len(managedTopicRoles) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := store.get(42, TopicRequests); got <= 0 || got == 77 {
		t.Fatalf("missing topic was not repaired: thread=%d created=%v", got, api.created)
	}
	if len(api.generalSends) != 1 {
		t.Fatalf("repair duplicated current notification in General: %v", api.generalSends)
	}
	if health := runtime.Health(); health.TopicReconcilePending || health.TopicLastError != "" || health.TopicCount != len(managedTopicRoles) {
		t.Fatalf("repaired topic health=%#v", health)
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

func TestCorruptTopicStoreBlocksAutomaticCreationUntilExplicitRepair(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "telegram-topics.json")
	if err := os.WriteFile(path, []byte(`{"topics":{"42":{"requests":0}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := &topicTestAPI{nextThreadID: 700}
	runtime := &Runtime{
		root: root, api: api,
		config: config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, TopicsEnabled: true},
	}
	runtime.reconcileTopicsBounded(t.Context(), api, runtime.config, true)
	health := runtime.Health()
	if health.TopicsEffective || health.TopicStoreHealthy || health.TopicLastError == "" || len(api.created) != 0 {
		t.Fatalf("corrupt store silently reconciled health=%#v created=%v", health, api.created)
	}
	if err := runtime.RepairTopics(t.Context()); err != nil {
		t.Fatal(err)
	}
	health = runtime.Health()
	if !health.TopicsEffective || !health.TopicStoreHealthy || health.TopicLastError != "" || health.TopicCount != len(managedTopicRoles) {
		t.Fatalf("repaired corrupt store health=%#v", health)
	}
	if len(api.created) != len(managedTopicRoles) {
		t.Fatalf("explicit repair created=%v", api.created)
	}
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatalf("corrupt metadata was not quarantined: %v", err)
	}
}

func TestIncomingThreadIDNeverChangesAuthorizationOrManagedRouting(t *testing.T) {
	cfg := config.TelegramConfig{AllowedUserIDs: []int64{42}}
	for _, threadID := range []int{0, 123, 999999} {
		authorized := Update{Message: &Message{MessageID: 1, MessageThreadID: threadID, From: &User{ID: 42}, Chat: Chat{ID: 42, Type: "private"}}}
		if !authorizedUpdate(cfg, authorized) {
			t.Fatalf("authorized private message rejected for thread=%d", threadID)
		}
		unauthorized := Update{Message: &Message{MessageID: 2, MessageThreadID: threadID, From: &User{ID: 43}, Chat: Chat{ID: 43, Type: "private"}}}
		if authorizedUpdate(cfg, unauthorized) {
			t.Fatalf("thread=%d granted unauthorized user access", threadID)
		}
		callback := Update{CallbackQuery: &CallbackQuery{ID: "cb", From: User{ID: 42}, Message: &Message{MessageID: 3, MessageThreadID: threadID, Chat: Chat{ID: 42, Type: "private"}}}}
		if !authorizedUpdate(cfg, callback) {
			t.Fatalf("authorized callback rejected for thread=%d", threadID)
		}
	}
}

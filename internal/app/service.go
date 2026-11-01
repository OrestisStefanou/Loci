package app

import "context"

// Service is every operation, as adapters see it. *App implements it inside
// loci serve; rpc.Client implements it in every other process by forwarding
// to loci serve over its socket. Adapters depend on Service, never on *App.
//
// Adding an operation means adding it here, which the compiler then requires
// of rpc.Client too.
type Service interface {
	SetTaskProject(ctx context.Context, in SetTaskProjectInput) (SetTaskProjectOutput, error)
	List(ctx context.Context, in ListInput) (ListOutput, error)
	Remember(ctx context.Context, in RememberInput) (RememberOutput, error)
	SearchMemories(ctx context.Context, in SearchMemoriesInput) (SearchMemoriesOutput, error)
	Embed(ctx context.Context, in EmbedInput) (EmbedOutput, error)
	Mark(ctx context.Context, in MarkInput) (MarkOutput, error)
	Attach(ctx context.Context, in AttachInput) (AttachOutput, error)
	CaptureStart(ctx context.Context, in CaptureStartInput) (CaptureStartOutput, error)
	CaptureMessage(ctx context.Context, in CaptureMessageInput) (CaptureMessageOutput, error)
	SearchConversations(ctx context.Context, in SearchConversationsInput) (SearchConversationsOutput, error)
	GetConversation(ctx context.Context, in GetConversationInput) (GetConversationOutput, error)
}

var _ Service = (*App)(nil)

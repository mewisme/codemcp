# History scope

Reserved for coordinated durable-history ownership: execution, checkpoint, completion, and other append/archive histories.

Live runtime mechanics may depend on durable history storage. History packages must not depend on live interface adapters. Existing checkpoint storage remains transitional until its history migration can preserve released state safely.

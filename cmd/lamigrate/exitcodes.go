package main

// Exit codes for lamigrate CLI.
const (
	ExitSuccess     = 0 // Command completed successfully.
	ExitExecution   = 1 // General runtime / execution error.
	ExitUsage       = 2 // Bad arguments, unknown command, or flag misuse.
	ExitLockTimeout = 3 // Advisory lock could not be acquired in time.
	ExitDirtyState  = 4 // Migration state is inconsistent (e.g. partially applied batch).
)

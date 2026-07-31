# lamigrate task-card protocol

Each card in this directory is one bounded, independently reviewable work item derived from `architecture.md`.

## Assignment rules

- Assign one card to one implementation agent in one dedicated worktree.
- The assignee reads the card, relevant architecture sections, current repository state, and existing tests before editing.
- A card may name likely files, but the architecture and acceptance criteria are authoritative.
- Do not start a `BLOCKED` or `DRAFT` card as implementation work.
- Do not change requirements merely to make the current prototype pass. Record a finding and return it to the coordinator.
- Do not commit or push unless expressly authorized by the maintainer.

## Card handoff format

An agent completing a card provides:

1. changed files and a concise behavioral summary;
2. exact verification commands and results;
3. architecture section coverage;
4. review findings and fixes, if any;
5. new blockers, decisions, or follow-up cards needed;
6. an explicit recommendation: `READY FOR REVIEW`, `BLOCKED`, or `DONE`.

## Shared-file merge policy

The listed cards are deliberately narrow but several eventually touch `lamigrate.go`, `file.go`, and `cmd/lamigrate/main.go`. Agents work in separate worktrees; the coordinator merges in dependency order and reruns integration tests after every contract-changing merge. No agent force-resolves another agent’s conflict without reviewing that card’s acceptance criteria.

## Security rule

Do not put live DSNs, passwords, database dumps, or tokens in task cards, tests, committed configuration, screenshots, or logs. Use isolated test credentials and safe placeholders only.

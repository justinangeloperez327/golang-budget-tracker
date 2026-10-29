-- +goose Up
-- Reminders are presentation state; they never alter financial history.
CREATE TABLE reminder_dismissals (
 workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('recurring','contribution')),
 resource_id TEXT NOT NULL CHECK(length(resource_id)=32),
 due_on DATE NOT NULL CHECK(due_on BETWEEN DATE '2000-01-01' AND DATE '2100-12-31'),
 dismissed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,kind,resource_id,due_on)
);
CREATE INDEX reminder_dismissals_recent ON reminder_dismissals(workspace_id,dismissed_at DESC);
-- +goose Down
DROP TABLE reminder_dismissals;

-- Optional free-text note on transfers (e.g. "Dinner split"), shown in history.
ALTER TABLE transactions ADD COLUMN note TEXT NOT NULL DEFAULT '';

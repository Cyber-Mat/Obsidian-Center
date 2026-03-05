CREATE TABLE vault_links (
    vault_id    TEXT NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
    source_path TEXT NOT NULL,
    target_path TEXT NOT NULL,
    link_text   TEXT,
    anchor      TEXT,
    is_embed    BOOLEAN DEFAULT FALSE,
    position    INTEGER,
    PRIMARY KEY (vault_id, source_path, target_path, position)
);

CREATE INDEX idx_links_target ON vault_links(vault_id, target_path);
CREATE INDEX idx_links_source ON vault_links(vault_id, source_path);

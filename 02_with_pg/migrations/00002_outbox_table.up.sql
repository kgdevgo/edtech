CREATE TABLE IF NOT EXISTS outbox_events (
                                             id UUID PRIMARY KEY,
                                             topic VARCHAR(255) NOT NULL,
                                             payload JSONB NOT NULL,
                                             status VARCHAR(50) NOT NULL DEFAULT 'pending',
                                             created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_outbox_events_status ON outbox_events(status) WHERE status = 'pending';
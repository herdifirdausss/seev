-- Dispute lifecycle notifications are mandatory in-app and immediate on
-- optional external channels. The Ledger event carries only bounded status,
-- amount, deadline, and reference metadata; evidence contents stay in Ledger.
INSERT INTO notif_templates(id, kind, description, variable_schema)
VALUES (
    '10000000-0000-0000-0000-000000000007',
    'compliance.dispute.lifecycle',
    'Chargeback dispute lifecycle update',
    '{"version":1,"variables":["amount","dispute","action"]}'
)
ON CONFLICT (kind) DO NOTHING;

INSERT INTO notif_template_versions(
    id, template_id, channel, locale, version, status, subject_template,
    title_template, body_text_template, body_html_template, content_hash,
    created_by, published_at
)
VALUES
(
    '20000000-0000-0000-0000-000000000017',
    '10000000-0000-0000-0000-000000000007',
    'in_app', 'en-US', 1, 'active',
    NULL, 'Dispute update',
    'Dispute {{.Dispute.Reference}} is now {{.Dispute.Status}} for {{.Amount.Display}}.{{if .Dispute.EvidenceDueAt}} Evidence is due by {{.Dispute.EvidenceDueAt}}.{{end}}',
    NULL, decode(md5('dispute-lifecycle-in-app-v1'),'hex'), 'system', now()
),
(
    '20000000-0000-0000-0000-000000000018',
    '10000000-0000-0000-0000-000000000007',
    'email', 'en-US', 1, 'active',
    'Your dispute has been updated', 'Dispute update',
    'Dispute {{.Dispute.Reference}} is now {{.Dispute.Status}} for {{.Amount.Display}}.{{if .Dispute.EvidenceDueAt}} Evidence is due by {{.Dispute.EvidenceDueAt}}.{{end}}',
    '<p>Dispute {{.Dispute.Reference}} is now {{.Dispute.Status}} for {{.Amount.Display}}.{{if .Dispute.EvidenceDueAt}} Evidence is due by {{.Dispute.EvidenceDueAt}}.{{end}}</p>',
    decode(md5('dispute-lifecycle-email-v1'),'hex'), 'system', now()
),
(
    '20000000-0000-0000-0000-000000000019',
    '10000000-0000-0000-0000-000000000007',
    'push', 'en-US', 1, 'active',
    NULL, 'Dispute update', 'Open Seev to review your dispute update.', NULL,
    decode(md5('dispute-lifecycle-push-v1'),'hex'), 'system', now()
)
ON CONFLICT (template_id, channel, locale, version) DO NOTHING;

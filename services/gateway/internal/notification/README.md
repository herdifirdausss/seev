# Notification module

Gateway Notifications owns the user inbox, notification preferences,
templates, channel planning, and delivery workers.

- `application/` — notification workflows, event consumer, HTTP/admin
  handlers, privacy, retention, and delivery orchestration.
- `model/` — notification and delivery data types.
- `repository/` — Gateway-owned notification persistence.
- `registry/` — notification-kind and event mapping.
- `template/` — safe template rendering and built-in fixtures.
- `channel/` — email and push provider contracts/adapters.

`module.go` is the stable facade used by Gateway composition. It contains no
business logic.

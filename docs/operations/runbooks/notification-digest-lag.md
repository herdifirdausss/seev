# Notification digest lag

Impact: daily email may arrive late or be suppressed when empty; in-app notices are immediate and money movement is unaffected.

Diagnosis: inspect due windows, local timezone/window boundaries, digest control, scheduler lease expiry, Auth contact resolution, and email backlog.

Safe action: do not create a second window manually. Recover the leased window or resume the digest control after checking the configured local time.

Recovery: let the unique window/delivery constraints converge, then replay only a dead/blocked delivery when justified.

Replay warning: a provider acceptance crash can duplicate the digest email.

Verify: one window per user/date/timezone, one digest delivery, item cap/more-count, schedule lag, and sanitized metrics.

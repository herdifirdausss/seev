# Notification template missing

Impact: mandatory in-app planning fails closed; optional email/push work is blocked. Money movement is unaffected.

Diagnosis: inspect the registry kind, channel, locale, active version, and template hash. Do not use a raw-event or arbitrary-send workaround.

Safe action: pause the affected external channel if needed, publish a reviewed fixture-valid version with maker/checker separation, then resume.

Recovery: replay only blocked deliveries after the active version is verified; the stored in-app snapshot is never rewritten.

Replay warning: replaying a provider-accepted delivery can duplicate external mail/push.

Verify: render the fixture, confirm one active version per locale/channel, inspect blocked count, and record the version ID and audit reference without rendered user data.

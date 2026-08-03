# Redpanda CDC log

Redpanda is a CDC transport/replay log, not a RabbitMQ replacement. Topics are
one partition in the initial local slice to make source ordering and offset
recovery observable. Data retention is bounded to seven days; a rebuild after
expiry requires a new approved source snapshot.

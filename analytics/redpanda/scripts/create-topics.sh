#!/usr/bin/env sh
set -eu

broker=${ANALYTICS_REDPANDA_BROKER:-redpanda:9092}
for topic in \
  seev.cdc.ledger.public.accounts.v1 \
  seev.cdc.ledger.public.account_balances.v1 \
  seev.cdc.ledger.public.ledger_transactions.v1 \
  seev.cdc.ledger.public.ledger_entries.v1 \
  seev.cdc.ledger.public.fee_quotes.v1 \
  seev.cdc.payin.public.payin_topup_intents.v1 \
  seev.cdc.payin.public.payin_webhook_events.v1 \
  seev.cdc.payout.public.payout_requests.v1 \
  seev.cdc.payout.public.payout_vendor_calls.v1; do
  rpk topic create "$topic" --brokers "$broker" --partitions 1 --replicas 1 --topic-config retention.ms=604800000 --topic-config cleanup.policy=delete --if-not-exists
done

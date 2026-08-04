// seed.js provisions real business state through the same APIs
// scripts/business-e2e.sh proves work (register, login, topup, signed
// mockvendor webhook) — Phase 0 §24.1: a canonical scenario must not
// require the operator to hand-supply SEEV_LOAD_TOKEN/SEEV_LOAD_TARGET_USER_ID
// before every run. Runs once inside k6's setup(), never per-VU.
import http from 'k6/http';
import { sleep } from 'k6';
import { authBaseURL, authInternalBaseURL, baseURL, vendorBaseURL, payinAdminBaseURL, ledgerBaseURL, signature } from './common.js';

const ADMIN_EMAIL = __ENV.LOAD_ADMIN_EMAIL || '';
const ADMIN_PASSWORD = __ENV.LOAD_ADMIN_PASSWORD || '';
const VENDOR_SECRET = __ENV.LOAD_VENDOR_SECRET || '';
const VENDOR2_SECRET = __ENV.LOAD_VENDOR2_SECRET || '';
const RUN_ID = __ENV.LOAD_RUN_ID || 'unbound';
const VENDOR_SECRETS = { mockvendor: VENDOR_SECRET, mockvendor2: VENDOR2_SECRET };

// mustJSON unwraps internal/platform/transport/http/response's standard envelope ({success, data:{...}})
// that every handler used here returns — every call site below reads the
// inner shape (e.g. body.user.id, body.tokens.access_token) directly.
function mustJSON(response, label) {
  if (response.status < 200 || response.status >= 300) {
    throw new Error(`${label} failed: status=${response.status} body=${response.body}`);
  }
  return response.json().data;
}

// parseDurationSeconds converts a k6 duration string ("30s", "1m", "1h2m3s")
// into whole seconds. Only h/m/s components are supported — that covers
// every LOAD_DURATION value this harness actually uses.
export function parseDurationSeconds(duration) {
  const match = String(duration).match(/^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$/);
  if (!match) return 60;
  const [, h, m, s] = match;
  return (Number(h) || 0) * 3600 + (Number(m) || 0) * 60 + (Number(s) || 0);
}

function adminLogin() {
  if (!ADMIN_EMAIL || !ADMIN_PASSWORD) {
    throw new Error('LOAD_ADMIN_EMAIL/LOAD_ADMIN_PASSWORD are required to self-seed this scenario');
  }
  const resp = http.post(`${authBaseURL}/api/v1/auth/login`, JSON.stringify({ email: ADMIN_EMAIL, password: ADMIN_PASSWORD }), { headers: { 'Content-Type': 'application/json' } });
  return mustJSON(resp, 'admin login').tokens.access_token;
}

// ensureTopupRoute makes the mockvendor topup route active, matching
// scripts/business-e2e.sh's onboarding sequence. Idempotent: skips creation
// when an enabled topup/mockvendor rule already exists (a fresh disposable
// stack never has one, but a run reusing SEEV_LOAD_KEEP_STACK might).
function ensureTopupRoute(adminToken) {
  const adminHeaders = { 'Content-Type': 'application/json', Authorization: `Bearer ${adminToken}` };
  http.put(`${payinAdminBaseURL}/admin/payin/vendor-gateways/mockvendor`, JSON.stringify({ gateway: 'bca' }), { headers: adminHeaders });
  const existing = mustJSON(http.get(`${payinAdminBaseURL}/admin/payin/routing-rules`, { headers: adminHeaders }), 'list routing rules');
  const active = (existing.rules || []).some((rule) => rule.flow === 'topup' && rule.vendor === 'mockvendor' && rule.enabled);
  if (active) return;
  mustJSON(http.post(`${payinAdminBaseURL}/admin/payin/routing-rules`, JSON.stringify({ flow: 'topup', priority: 10, enabled: true, currency: 'IDR', vendor: 'mockvendor' }), { headers: adminHeaders }), 'create routing rule');
}

function registerUser(email, password) {
  const resp = http.post(`${authBaseURL}/api/v1/auth/register`, JSON.stringify({ email, password, full_name: 'Load Test User' }), { headers: { 'Content-Type': 'application/json' } });
  const body = mustJSON(resp, `register ${email}`);
  return { id: body.user.id, token: body.tokens.access_token };
}

// login is exported: w4-mixed.js's "login or refresh" category calls this
// directly, once per matching iteration — see seedMixedPool for why it's
// login on every occurrence rather than a captured refresh_token reused
// across iterations.
export function login(email, password) {
  const resp = http.post(`${authBaseURL}/api/v1/auth/login`, JSON.stringify({ email, password }), { headers: { 'Content-Type': 'application/json' } });
  return mustJSON(resp, `login ${email}`).tokens.access_token;
}

// approveKyc submits a level-1 KYC request as the user and, unless the
// synchronous provider already auto-decided it, approves it as admin on
// auth-service's internal mTLS listener — same two-call sequence
// scripts/business-e2e.sh proves works. Topup requires kyc_level >= 1
// (discovered live: a fresh registrant is kyc_level 0 and topup returns
// 403 KYC_REQUIRED without this step).
//
// Approval's tier application is not always synchronous: when
// ApplyKycTier's fast path fails (e.g. ledger briefly unreachable),
// AdminApproveKYCHandler returns 202 {"status":"queued"} and the actual
// kyc_level bump lands later via auth's own outbox retry — discovered live,
// the same request that returns 202 leaves a fresh disposable stack's
// kyc_level still at 0 for a beat. Poll the user's own status until it
// catches up instead of assuming approve's HTTP response means "applied".
function approveKyc(userToken, adminToken) {
  const submit = mustJSON(http.post(`${authBaseURL}/api/v1/users/me/kyc`, JSON.stringify({ level_requested: 1 }), { headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${userToken}` } }), 'submit kyc');
  if (submit.status === 'approved') return;
  const adminHeaders = { 'Content-Type': 'application/json', Authorization: `Bearer ${adminToken}` };
  const approve = http.post(`${authInternalBaseURL}/api/v1/admin/kyc/submissions/${submit.id}/approve`, '{}', { headers: adminHeaders });
  if (approve.status < 200 || approve.status >= 300) {
    throw new Error(`kyc approval failed: status=${approve.status} body=${approve.body}`);
  }
  const userHeaders = { Authorization: `Bearer ${userToken}` };
  for (let attempt = 0; attempt < 20; attempt++) {
    const status = mustJSON(http.get(`${authBaseURL}/api/v1/users/me/kyc`, { headers: userHeaders }), 'kyc status');
    if (status.kyc_level >= 1) return;
    sleep(0.5);
  }
  throw new Error('kyc approval accepted but kyc_level never reached 1 within timeout');
}

// createTopupIntent creates a pending topup intent as the given user and
// returns {reference, vendor} — vendor is whichever payin's routing engine
// actually assigned (the caller never picks it directly), which is exactly
// what W5 needs to know which vendor's webhook path/secret to use.
// Exported: w4-mixed.js creates one per iteration (not a pre-seeded pool —
// W4's topup action is a complete per-iteration journey, unlike W2/W5).
export function createTopupIntent(userToken, amount) {
  const create = mustJSON(http.post(`${baseURL}/api/v1/topup`, JSON.stringify({ amount: String(amount) }), { headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${userToken}` } }), 'create topup intent');
  return { reference: create.reference, vendor: create.vendor, amount };
}

// deliverWebhook signs and sends the mockvendor callback that settles a
// pending topup intent — the same shape scripts/business-e2e.sh proves
// works. user_id in the body is a fixed placeholder — VendorService
// normalizes it away and Payin uses the intent's owner. eventID must be
// unique per delivery (not per reference) so a scenario replaying an
// intent's reference for a redelivery test doesn't collide with the
// original settlement's event_id. Exported: w5-hotspot.js calls this
// directly per-iteration, since the load phase IS the webhook delivery.
export function deliverWebhook(vendor, reference, amount, eventID) {
  const secret = VENDOR_SECRETS[vendor];
  if (!secret) throw new Error(`no webhook secret configured for vendor ${vendor}`);
  const body = JSON.stringify({
    event_id: eventID,
    external_ref: reference,
    user_id: '00000000-0000-0000-0000-000000000000',
    amount: String(amount),
    currency: 'IDR',
    occurred_at: new Date().toISOString(),
    type: 'payment.settled',
  });
  const sig = signature(secret, body);
  return http.post(`${vendorBaseURL}/webhooks/${vendor}`, body, { headers: { 'Content-Type': 'application/json', 'X-Mock-Signature': sig } });
}

// fundUser tops the given user up by amount minor units: create an intent,
// then immediately settle it via its assigned vendor's signed webhook.
function fundUser(userToken, amount) {
  const intent = createTopupIntent(userToken, amount);
  const webhook = deliverWebhook(intent.vendor, intent.reference, amount, `loadseed-${RUN_ID}-${intent.reference}`);
  if (webhook.status < 200 || webhook.status >= 300) {
    throw new Error(`topup webhook failed: status=${webhook.status} body=${webhook.body}`);
  }
}

// ensureVendorOverride pins a specific user's topups to a specific vendor
// via payin's per-user routing override (routingRulePayload.UserID) —
// higher-specificity than the general priority-10 default rule, per the
// routing engine's user-override-first resolution (docs/roadmap/archive/29/30).
// This is how W5's two-gateway control forces half its pool onto a second
// system settlement account without touching the default rule at all.
//
// payin_routing_rules.vendor has a hard FK to payin_vendor_gateways(vendor)
// (services/payin/migrations/000003_routing.up.sql), which seeds only "mockvendor"
// by default — creating a rule for an unregistered vendor violates that FK
// and surfaces as a generic 500 (discovered live). The vendor-gateways PUT
// is upsert-idempotent, same as ensureTopupRoute's call for mockvendor.
function ensureVendorOverride(adminToken, userId, vendor) {
  const adminHeaders = { 'Content-Type': 'application/json', Authorization: `Bearer ${adminToken}` };
  http.put(`${payinAdminBaseURL}/admin/payin/vendor-gateways/${vendor}`, JSON.stringify({ gateway: 'bca' }), { headers: adminHeaders });
  mustJSON(http.post(`${payinAdminBaseURL}/admin/payin/routing-rules`, JSON.stringify({ flow: 'topup', priority: 1, enabled: true, currency: 'IDR', vendor, user_id: userId }), { headers: adminHeaders }), `create vendor override for ${userId}`);
}

// policy_tier_limits (services/ledger/migrations/000022_policy_tier_limits.up.sql)
// caps a KYC level-1 account at 20 transfer_p2p / 5 withdraw_initiate per
// day, enforced on the PUBLIC user-initiated posting endpoints —
// discovered live: a single shared sender doing repeated transfers or
// payouts eventually got 422 "policy limit exceeded (max_daily_count)".
// scripts/load-postgres-init/04-load-policy-limits.sh raises those limits
// to effectively unbounded in the disposable load database only, so pool
// size below is a pure testing-realism knob — concurrent traffic across
// multiple distinct accounts, matching K7's "W1 uses disjoint funded
// account pairs" — not a workaround for a policy ceiling.
//
// concurrencyPoolSize sizes an account pool for realistic multi-account
// concurrent traffic: several genuinely different accounts transacting
// with each other, not one pair hit repeatedly. A deliberately small,
// fixed pool (e.g. via SEEV_LOAD_HOTSPOT_VARIANT) is exactly how W5 tests
// the opposite end of this spectrum — this default just avoids that
// extreme by accident.
function concurrencyPoolSize(estimatedIterations) {
  return Math.min(Math.max(Math.ceil(estimatedIterations / 5), 4), 50);
}

// seedP2PPair creates a POOL of funded sender/recipient pairs for W1 —
// K7's own intent ("W1 uses disjoint funded account pairs") that a single
// pair violated. fundAmount is sized per sender's own share of the run's
// iterations, not the whole run, plus a 3x safety margin.
export function seedP2PPair(amountPerTransfer = 1000) {
  const adminToken = adminLogin();
  ensureTopupRoute(adminToken);
  const rate = Number(__ENV.LOAD_RATE || 1);
  const seconds = parseDurationSeconds(__ENV.LOAD_DURATION || '1m');
  const totalIterations = Math.ceil(rate * seconds);
  const pairCount = concurrencyPoolSize(totalIterations);
  const fundAmount = Math.min(Math.max(Math.ceil(totalIterations / pairCount) * amountPerTransfer * 3, 100000), 500000000);
  const pairs = [];
  for (let index = 0; index < pairCount; index++) {
    const senderEmail = `load-p2p-${RUN_ID}-sender${index}@example.invalid`;
    const senderPassword = 'LoadSeedP2P2026';
    const sender = registerUser(senderEmail, senderPassword);
    const recipient = registerUser(`load-p2p-${RUN_ID}-recipient${index}@example.invalid`, 'LoadSeedP2P2026');
    approveKyc(sender.token, adminToken);
    // The sender's registration-time JWT still carries kyc_level=0 as a
    // claim — gateway's requireKYC middleware reads that claim, not a live
    // DB lookup, so it stays 403 KYC_REQUIRED until a fresh token is minted
    // (discovered live: approveKyc's own DB-level poll passes while topup
    // with the same old token still 403s). Re-login for a token that
    // reflects the just-approved level before funding or returning it.
    const freshToken = login(senderEmail, senderPassword);
    fundUser(freshToken, fundAmount);
    pairs.push({ token: freshToken, targetUserId: recipient.id });
  }
  return { pairs, fundAmount };
}

// seedHotspotPool pre-creates `poolSize` PENDING topup intents (K7/K11's W5: "W2 against
// one gateway versus an evenly split two-gateway control"). variant "one"
// routes every intent through the single default mockvendor rule, so every
// webhook in the load phase updates the SAME system settlement account row
// — the canonical lock-contention condition K11 measures. variant "two"
// additionally registers a second user pinned to mockvendor2 via a routing
// override and splits the pool evenly across both users/vendors, so the
// same webhook volume spreads across two independent system-account rows.
// The load phase (w5-hotspot.js) delivers the actual settlement webhooks —
// setup() only creates the pending intents so iteration latency measures
// webhook/lock contention, not intent-creation cost.
export function seedHotspotPool(variant, amountPerIntent = 1000) {
  const adminToken = adminLogin();
  ensureTopupRoute(adminToken);
  const rate = Number(__ENV.LOAD_RATE || 1);
  const seconds = parseDurationSeconds(__ENV.LOAD_DURATION || '1m');
  const poolSize = Math.min(Math.max(Math.ceil(rate * seconds * 1.2), 4), 200);

  const primaryEmail = `load-hotspot-${RUN_ID}-a@example.invalid`;
  const primaryPassword = 'LoadSeedHotspot2026';
  const primary = registerUser(primaryEmail, primaryPassword);
  approveKyc(primary.token, adminToken);
  const primaryToken = login(primaryEmail, primaryPassword);

  let secondaryToken = null;
  if (variant === 'two') {
    const secondaryEmail = `load-hotspot-${RUN_ID}-b@example.invalid`;
    const secondaryPassword = 'LoadSeedHotspot2026';
    const secondary = registerUser(secondaryEmail, secondaryPassword);
    approveKyc(secondary.token, adminToken);
    ensureVendorOverride(adminToken, secondary.id, 'mockvendor2');
    secondaryToken = login(secondaryEmail, secondaryPassword);
  }

  const intents = [];
  for (let index = 0; index < poolSize; index++) {
    const token = secondaryToken && index % 2 === 1 ? secondaryToken : primaryToken;
    intents.push(createTopupIntent(token, amountPerIntent));
  }
  return { variant, intents, amountPerIntent };
}

// seedPayoutSender registers, KYC-approves, and funds a POOL of senders for
// W3, for realistic multi-account concurrent traffic. Payout's own default
// routing rule is seeded by services/payout/migrations/000002_routing.up.sql,
// unlike payin's topup route, so there is no ensurePayoutRoute step here.
export function seedPayoutSender(amountPerPayout = 1000) {
  const adminToken = adminLogin();
  ensureTopupRoute(adminToken);
  const rate = Number(__ENV.LOAD_RATE || 1);
  const seconds = parseDurationSeconds(__ENV.LOAD_DURATION || '1m');
  const totalIterations = Math.ceil(rate * seconds);
  const senderCount = concurrencyPoolSize(totalIterations);
  const fundAmount = Math.min(Math.max(Math.ceil(totalIterations / senderCount) * amountPerPayout * 3, 100000), 500000000);
  const senders = [];
  for (let index = 0; index < senderCount; index++) {
    const email = `load-payout-${RUN_ID}-${index}@example.invalid`;
    const password = 'LoadSeedPayout2026';
    const user = registerUser(email, password);
    approveKyc(user.token, adminToken);
    const freshToken = login(email, password);
    fundUser(freshToken, fundAmount);
    senders.push(freshToken);
  }
  return { senders, amountPerPayout };
}

// seedWebhookPool pre-creates one pending topup intent per planned
// iteration for W2 (K7: "one valid signed callback for a pre-created
// intent" plus "a separately tagged 10% exact webhook redelivery stream").
// One registered user owns the whole pool — W2 measures webhook/settlement
// contention and duplicate-delivery handling, not multi-account routing
// (that is W5's job). Settlement correctness (no duplicate financial
// effect from the redelivery stream) is verified by the ledger integrity
// check scripts/load-test.sh already runs after every load phase (Phase 0
// §24.3) — this scenario does not additionally poll each intent's status
// per iteration, which would conflate settlement-confirmation latency with
// webhook-handler latency in the same measurement.
export function seedWebhookPool(amountPerIntent = 1000) {
  const adminToken = adminLogin();
  ensureTopupRoute(adminToken);
  const rate = Number(__ENV.LOAD_RATE || 1);
  const seconds = parseDurationSeconds(__ENV.LOAD_DURATION || '1m');
  const poolSize = Math.min(Math.max(Math.ceil(rate * seconds * 1.2), 4), 200);

  const email = `load-webhook-${RUN_ID}@example.invalid`;
  const password = 'LoadSeedWebhook2026';
  const user = registerUser(email, password);
  approveKyc(user.token, adminToken);
  const token = login(email, password);

  const intents = [];
  for (let index = 0; index < poolSize; index++) {
    intents.push(createTopupIntent(token, amountPerIntent));
  }
  return { intents, amountPerIntent };
}

// seedMixedPool registers a POOL of sender/recipient pairs for W4 (K8's
// fixed weights: 35% quote+transfer, 20% read, 20% topup+webhook, 15%
// payout, 5% notifications, 5% login), for realistic multi-account
// concurrent traffic. fundAmount uses a wider 5x margin than W1/W3's 3x
// since each pool member is net-debited by two action types (transfer +
// payout) against one credit type (topup).
//
// Each pool entry carries its own email/password for the "login or
// refresh" category — a fresh login every occurrence, not a captured
// refresh_token: refresh tokens are one-time-use, and reusing an
// already-consumed one is treated as replay and revokes EVERY token for
// that user (services/auth.go:258-262) — including the access_token
// every other category in this scenario depends on. Discovered live: a
// captured refresh_token reused across a run's several "refresh" slots
// cascaded into failures across unrelated categories. Repeatable login has
// no such one-time constraint and satisfies K8's "or" either way.
export function seedMixedPool(amountPerAction = 1000) {
  const adminToken = adminLogin();
  ensureTopupRoute(adminToken);
  const rate = Number(__ENV.LOAD_RATE || 1);
  const seconds = parseDurationSeconds(__ENV.LOAD_DURATION || '1m');
  const totalIterations = Math.ceil(rate * seconds);
  const poolSize = concurrencyPoolSize(totalIterations);
  const fundAmount = Math.min(Math.max(Math.ceil(totalIterations / poolSize) * amountPerAction * 5, 200000), 500000000);

  const senders = [];
  for (let index = 0; index < poolSize; index++) {
    const email = `load-mixed-${RUN_ID}-a${index}@example.invalid`;
    const password = 'LoadSeedMixed2026';
    const sender = registerUser(email, password);
    const recipient = registerUser(`load-mixed-${RUN_ID}-b${index}@example.invalid`, 'LoadSeedMixed2026');
    approveKyc(sender.token, adminToken);
    const freshToken = login(email, password);
    fundUser(freshToken, fundAmount);
    senders.push({ token: freshToken, targetUserId: recipient.id, email, password });
  }
  return { senders, amountPerAction };
}

// seedDisbursementBatches provisions `batchCount` disbursement batches (via
// the real admin CSV-upload API, matching this file's own pattern of
// proving real business state through real endpoints) for the burst
// hot-account experiment: every disbursement item, regardless of batch,
// debits the SAME singleton settlement[platform][currency] account
// (services/ledger/migrations/000015_disbursement.up.sql) — a different, untested
// contention point from W5's settlement[gateway] account. Recipients are a
// small, reused pool (they are credited, not the contended side, so unlike
// W1/W3/W4's sender pools they don't need to be sized 1:1 with item count).
// itemsPerBatch defaults well under runDisbursement's 500-per-call cap
// (services/ledger/internal/ledger/disbursement/disbursement.go) so a single
// POST /run drains a whole batch in one call — the load phase's workload
// unit is "one batch fully run," not "one item."
export function seedDisbursementBatches(batchCount = 20, itemsPerBatch = 100, amountPerItem = 1000) {
  const adminToken = adminLogin();
  // A second, distinct identity for the approve step below — a maker-checker
  // gate added later in the same session that introduced this scenario
  // requires the approver to differ from the batch's creator (adminToken
  // above); deploy/load/compose.load.yaml's AUTH_BOOTSTRAP_CHECKER_EMAIL/
  // PASSWORD provisions it the same way AUTH_BOOTSTRAP_ADMIN_EMAIL/PASSWORD
  // provisions adminToken.
  const checkerEmail = __ENV.LOAD_CHECKER_EMAIL || '';
  const checkerPassword = __ENV.LOAD_CHECKER_PASSWORD || '';
  if (!checkerEmail || !checkerPassword) {
    throw new Error('LOAD_CHECKER_EMAIL/LOAD_CHECKER_PASSWORD are required to approve seeded disbursement batches');
  }
  const checkerToken = login(checkerEmail, checkerPassword);
  const recipientPoolSize = Math.min(Math.max(batchCount, 10), 50);
  const recipients = [];
  for (let index = 0; index < recipientPoolSize; index++) {
    const user = registerUser(`load-disburse-${RUN_ID}-r${index}@example.invalid`, 'LoadSeedDisburse2026');
    recipients.push(user.id);
  }

  const batchIds = [];
  for (let b = 0; b < batchCount; b++) {
    const rows = ['user_id,amount,note'];
    for (let i = 0; i < itemsPerBatch; i++) {
      const userId = recipients[(b * itemsPerBatch + i) % recipients.length];
      rows.push(`${userId},${amountPerItem},burst-${RUN_ID}-${b}-${i}`);
    }
    const form = { file: http.file(rows.join('\n'), `batch-${b}.csv`, 'text/csv') };
    // /api/v1/admin/ledger/disbursements (the path suggested by admin-bff's
    // own proxy, services/adminbff/internal/admin/module.go:117) 404s — discovered live:
    // services/ledger/cmd/ledger/main.go:338 mounts the admin routes with
    // `http.StripPrefix("/api/v1", ...)` under the OUTER pattern
    // "/api/v1/admin/ledger/", which only strips "/api/v1" and leaves
    // "/admin/ledger/disbursements" for a mux that only registers
    // "/admin/disbursements" (services/ledger/internal/transport/http.go:194) — a
    // real, apparently-never-exercised-end-to-end routing bug (existing
    // tests only call the inner mux directly, bypassing this mount
    // entirely). The OTHER mount at line 337, "/api/v1/ledger/" with
    // `StripPrefix("/api/v1/ledger", ...)`, strips its own full matched
    // prefix correctly, so the same handler IS reachable at
    // /api/v1/ledger/admin/disbursements — verified live (404 -> 400
    // business validation once routed correctly).
    const resp = http.post(`${ledgerBaseURL}/api/v1/ledger/admin/disbursements`, form, { headers: { Authorization: `Bearer ${adminToken}` } });
    const body = mustJSON(resp, `create disbursement batch ${b}`);
    // A batch starts 'pending_approval' (business-completeness audit
    // finding, services/ledger/migrations/000038) — POST .../run 409s until a SECOND
    // identity (checkerToken, never adminToken) approves it.
    mustJSON(http.post(`${ledgerBaseURL}/api/v1/ledger/admin/disbursements/${body.id}/approve`, '{}', { headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${checkerToken}` } }), `approve disbursement batch ${b}`);
    batchIds.push(body.id);
  }
  return { batchIds, amountPerItem, adminToken };
}

// seedResolverUser registers ONE user for w6-resolver.js (B3's experiment,
// docs/performance/reports/2026-xx-baseline.md §22) — the fee-quote endpoint
// it hits never moves money (createQuote's own doc comment) and doesn't need
// KYC/funding, and using the SAME user for every iteration is deliberate:
// B3 measures repeated-key cacheability, and a fixed userID maximizes the
// chance of hitting the exact same cache key across iterations (a fresh
// user per iteration would artificially inflate the key space and understate
// real-world cacheability, since ResolveRule's cache key includes userID).
export function seedResolverUser() {
  const email = `load-resolver-${RUN_ID}@example.invalid`;
  const user = registerUser(email, 'LoadSeedResolver2026');
  return { token: user.token };
}

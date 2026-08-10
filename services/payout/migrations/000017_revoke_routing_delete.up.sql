-- Routing is disabled or updated in place; the runtime has no direct DELETE
-- path and therefore does not need destructive table privileges.
REVOKE DELETE ON payout_vendor_gateways, payout_routing_rules FROM app_service;

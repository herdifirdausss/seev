-- Routing is disabled or updated in place; the runtime has no direct DELETE
-- path and therefore does not need destructive table privileges.
REVOKE DELETE ON payin_vendor_gateways, payin_routing_rules FROM app_service;

// Command metrics-exporter serves a bounded set of Prometheus metrics
// (plan 58 section 24.4) derived from ClickHouse control/mart tables and the
// Kafka Connect REST API. It is read-only against both: no source writes,
// no arbitrary SQL, no sensitive identifiers in labels (plan 58 section
// 20's restrictions apply here too, even though this is not the
// reconciliation CLI itself).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type config struct {
	ClickHouseURL  string
	ClickHouseUser string
	ClickHousePass string
	ConnectURL     string
	ListenAddr     string
}

func loadConfig() config {
	password := os.Getenv("ANALYTICS_CLICKHOUSE_PASSWORD")
	if file := os.Getenv("ANALYTICS_CLICKHOUSE_PASSWORD_FILE"); file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			log.Fatalf("read ANALYTICS_CLICKHOUSE_PASSWORD_FILE: %v", err)
		}
		password = strings.TrimSpace(string(raw))
	}
	return config{
		ClickHouseURL:  defaultString(os.Getenv("ANALYTICS_CLICKHOUSE_URL"), "http://127.0.0.1:8123"),
		ClickHouseUser: defaultString(os.Getenv("ANALYTICS_CLICKHOUSE_USER"), "analytics_reconciliation"),
		ClickHousePass: password,
		ConnectURL:     defaultString(os.Getenv("ANALYTICS_CONNECT_URL"), "http://127.0.0.1:18083"),
		ListenAddr:     defaultString(os.Getenv("ANALYTICS_METRICS_LISTEN_ADDR"), "127.0.0.1:9308"),
	}
}

func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func main() {
	cfg := loadConfig()
	if cfg.ClickHousePass == "" {
		log.Fatal("ANALYTICS_CLICKHOUSE_PASSWORD or ANALYTICS_CLICKHOUSE_PASSWORD_FILE is required")
	}

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		var b strings.Builder
		writeReconciliationMetrics(ctx, cfg, &b)
		writeFreshnessMetrics(ctx, cfg, &b)
		writeDbtMetrics(ctx, cfg, &b)
		writeConnectorMetrics(ctx, cfg, &b)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(b.String()))
	})

	log.Printf("seev_analytics metrics exporter listening on %s", cfg.ListenAddr)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, nil))
}

func clickHouseQuery(ctx context.Context, cfg config, query string) ([]byte, error) {
	endpoint, err := url.Parse(cfg.ClickHouseURL)
	if err != nil {
		return nil, err
	}
	values := endpoint.Query()
	values.Set("query", query)
	endpoint.RawQuery = values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(cfg.ClickHouseUser, cfg.ClickHousePass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("clickhouse query failed: %s: %s", resp.Status, string(body))
	}
	return body, nil
}

func writeReconciliationMetrics(ctx context.Context, cfg config, b *strings.Builder) {
	body, err := clickHouseQuery(ctx, cfg, "SELECT status, critical_failures, warning_failures FROM control.reconciliation_runs ORDER BY finished_at DESC LIMIT 1 FORMAT TabSeparated")
	if err != nil {
		log.Printf("reconciliation metrics: %v", err)
		return
	}
	fields := strings.Split(strings.TrimSpace(string(body)), "\t")
	if len(fields) != 3 {
		return
	}
	critical, _ := strconv.Atoi(fields[1])
	warning, _ := strconv.Atoi(fields[2])
	fmt.Fprintf(b, "# HELP seev_analytics_reconciliation_critical_failures Critical failures in the latest reconciliation run\n")
	fmt.Fprintf(b, "# TYPE seev_analytics_reconciliation_critical_failures gauge\n")
	fmt.Fprintf(b, "seev_analytics_reconciliation_critical_failures %d\n", critical)
	fmt.Fprintf(b, "# HELP seev_analytics_reconciliation_warning_failures Warning failures in the latest reconciliation run\n")
	fmt.Fprintf(b, "# TYPE seev_analytics_reconciliation_warning_failures gauge\n")
	fmt.Fprintf(b, "seev_analytics_reconciliation_warning_failures %d\n", warning)
	fmt.Fprintf(b, "# HELP seev_analytics_reconciliation_passed Whether the latest reconciliation run passed (1) or not (0)\n")
	fmt.Fprintf(b, "# TYPE seev_analytics_reconciliation_passed gauge\n")
	passed := 0
	if fields[0] == "passed" {
		passed = 1
	}
	fmt.Fprintf(b, "seev_analytics_reconciliation_passed %d\n", passed)
}

func writeFreshnessMetrics(ctx context.Context, cfg config, b *strings.Builder) {
	body, err := clickHouseQuery(ctx, cfg, "SELECT source_service, source_table, freshness_seconds FROM mart.mart_freshness FORMAT TabSeparated")
	if err != nil {
		log.Printf("freshness metrics: %v", err)
		return
	}
	fmt.Fprintf(b, "# HELP seev_analytics_data_freshness_seconds Seconds since the newest ingested row for a source table\n")
	fmt.Fprintf(b, "# TYPE seev_analytics_data_freshness_seconds gauge\n")
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			continue
		}
		seconds, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		fmt.Fprintf(b, "seev_analytics_data_freshness_seconds{source=%q,table=%q} %d\n", fields[0], fields[1], seconds)
	}
}

func writeDbtMetrics(ctx context.Context, cfg config, b *strings.Builder) {
	body, err := clickHouseQuery(ctx, cfg, "SELECT result, count() FROM control.dbt_invocations WHERE started_at >= now() - INTERVAL 1 DAY GROUP BY result FORMAT TabSeparated")
	if err != nil {
		log.Printf("dbt metrics: %v", err)
		return
	}
	fmt.Fprintf(b, "# HELP seev_analytics_dbt_run_total dbt invocations in the last 24h by result\n")
	fmt.Fprintf(b, "# TYPE seev_analytics_dbt_run_total counter\n")
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 2 {
			continue
		}
		fmt.Fprintf(b, "seev_analytics_dbt_run_total{result=%q} %s\n", fields[0], fields[1])
	}
}

func writeConnectorMetrics(ctx context.Context, cfg config, b *strings.Builder) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.ConnectURL+"/connectors?expand=status", nil)
	if err != nil {
		log.Printf("connector metrics: %v", err)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("connector metrics: %v", err)
		return
	}
	defer resp.Body.Close()
	var payload map[string]struct {
		Status struct {
			Connector struct {
				State string `json:"state"`
			} `json:"connector"`
			Tasks []struct {
				State string `json:"state"`
			} `json:"tasks"`
		} `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		log.Printf("connector metrics: decode: %v", err)
		return
	}
	fmt.Fprintf(b, "# HELP seev_analytics_connector_up Whether a connector's tasks are all RUNNING (1) or not (0)\n")
	fmt.Fprintf(b, "# TYPE seev_analytics_connector_up gauge\n")
	for name, entry := range payload {
		up := 1
		if entry.Status.Connector.State != "RUNNING" {
			up = 0
		}
		for _, task := range entry.Status.Tasks {
			if task.State != "RUNNING" {
				up = 0
			}
		}
		fmt.Fprintf(b, "seev_analytics_connector_up{source=%q} %d\n", name, up)
	}
}

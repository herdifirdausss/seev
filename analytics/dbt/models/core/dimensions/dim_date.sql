{{ config(materialized='table') }}

-- numbers()'s argument is evaluated as a data source, before any FROM-clause
-- correlation exists, so it cannot reference a joined CTE's columns
-- (confirmed 2026-08-05: `UNKNOWN_IDENTIFIER` on start_date/end_date). The
-- day count (730 days back + today + 30 days forward) is a fixed constant,
-- so it is inlined directly instead of being derived via dateDiff() on a
-- cross-joined bounds CTE.
with dates as (
    select addDays(toDate(today() - 730), number) as report_date
    from numbers(730 + 30 + 1)
)
select
    report_date,
    toYear(report_date) as report_year,
    toMonth(report_date) as report_month,
    toDayOfMonth(report_date) as report_day,
    toDayOfWeek(report_date) as day_of_week,
    toDayOfWeek(report_date) IN (6, 7) as is_weekend
from dates

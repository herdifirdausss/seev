{{ config(materialized='table') }}

with bounds as (
    select
        toDate(today() - 730) as start_date,
        toDate(today() + 30) as end_date
),
dates as (
    select addDays(start_date, number) as report_date
    from bounds
    cross join numbers(dateDiff('day', start_date, end_date) + 1)
)
select
    report_date,
    toYear(report_date) as report_year,
    toMonth(report_date) as report_month,
    toDayOfMonth(report_date) as report_day,
    toDayOfWeek(report_date) as day_of_week,
    toDayOfWeek(report_date) IN (6, 7) as is_weekend
from dates

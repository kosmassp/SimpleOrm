-- Daily sales: one row per calendar day, most recent first.
-- Backs the statement-mapped entity SimpleOrm.Sample.Models.DailySales (ADR-0008).
select date(created_at) as sales_date,
       count(id)        as transaction_count,
       sum(amount)      as total_amount
from transactions
group by date(created_at)
order by sales_date desc

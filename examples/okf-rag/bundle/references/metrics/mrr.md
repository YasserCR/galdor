---
type: Metric
title: MRR (Monthly Recurring Revenue)
description: Monthly Recurring Revenue from active subscriptions.
tags: [metric, revenue, recurring, mrr]
timestamp: '2026-06-04T10:00:00Z'
---

# Definition

Monthly Recurring Revenue (MRR) is the normalized predictable revenue expected
each month from active subscriptions. It is modeled from the
[subscriptions](../../tables/subscriptions.md) table by summing `mrr_amount`
over subscriptions in the active or past_due state.

# Notes

Do not confuse MRR with recognized revenue; MRR is forward-looking and modeled.

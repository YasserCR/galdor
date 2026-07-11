---
type: Warehouse Table
title: Subscriptions
description: One row per subscription a customer holds, with plan and lifecycle state.
resource: warehouse://acme-prod/analytics/subscriptions
tags: [subscriptions, recurring, billing, mrr]
timestamp: '2026-06-02T11:35:00Z'
---

# Overview

Each row is one subscription. The `mrr_amount` column holds the normalized
monthly recurring revenue for that subscription. Only rows in the `active` or
`past_due` status contribute to live revenue metrics.

# Schema

| column | type | notes |
|--------|------|-------|
| subscription_id | string | primary key |
| status | string | active, past_due, canceled |
| mrr_amount | number | monthly recurring revenue |

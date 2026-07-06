# AGENTS.md

# Tesla Analytics Platform

## Mission

The objective of this project is to build a personal Tesla analytics platform that provides long-term insights into vehicle usage, battery health, charging habits, driving efficiency, operating costs, and overall vehicle performance.

This project is **not** intended to simply display Tesla API responses. Instead, it transforms raw Tesla telemetry into meaningful historical metrics, trends, forecasts, and dashboards that help the owner better understand the vehicle over months and years.

The platform should continuously evolve as Tesla exposes additional APIs or as new analytical ideas emerge.

---

# Guiding Principles

The project should always prioritize:

1. Historical data over current snapshots.
2. Long-term trends over individual events.
3. Derived metrics over raw API values.
4. Time-series analytics over transactional storage.
5. Extensibility for future Tesla APIs.
6. Technology independence.
7. High cohesion and loose coupling.
8. Testability.
9. Maintainability.
10. Low operational cost.

Raw Tesla API responses are only an intermediate step. The primary value of the platform comes from calculated insights.

---

# Primary Objectives

The platform should answer questions such as:

* Is my battery degrading normally?
* How has my battery capacity changed over time?
* How much energy do I consume per kilometer?
* How efficient is my driving?
* What affects my efficiency the most?
* How much money am I saving compared to a gasoline vehicle?
* What are my charging habits?
* How much phantom drain does the vehicle experience?
* How accurate are Tesla's range predictions?
* Are there unusual behaviors that deserve investigation?
* How does weather affect energy consumption?
* How does driving style affect efficiency?
* How often do I fast charge?
* How healthy are my charging habits?

The platform should always evolve toward answering more meaningful questions.

---

# Data Collection Strategy

The platform should identify Tesla APIs that provide valuable historical information.

Typical categories include:

* Battery information
* Charging sessions
* Vehicle state
* Drive state
* Climate state
* Vehicle configuration


Agents should continuously evaluate whether new Tesla APIs introduce useful analytical opportunities.

---

# Data Retention Philosophy

Prefer storing historical events rather than overwriting state.

Examples include:

* Battery snapshots
* Charging sessions
* Vehicle snapshots
* Climate snapshots
* Trips
* Daily or weekly summaries (The best that cannot consume too much tesla API quota)
* Monthly summaries

Historical information should never be discarded unless retention policies explicitly require it.

---

# Metrics

Whenever possible, derive metrics instead of storing only raw values.

Examples include:

Battery

* Estimated battery health
* Capacity degradation
* Range degradation
* Battery aging
* Battery efficiency

Charging

* Home charging ratio
* Other charging ration
* Supercharger ratio
* Charging efficiency
* Average charging speed
* Charging duration
* Energy added
* Charging costs

Driving

* Energy per kilometer
* Energy per trip
* Average speed
* Daily distance
* Monthly distance
* Seasonal efficiency
* Driving efficiency score

Cost

* Electricity costs
* Estimated gasoline equivalent
* Savings
* Cost per kilometer
* Cost per month

Vehicle Usage

* Daily utilization
* Idle time
* Sleep time
* Phantom drain
* Vehicle availability

Forecasting

* Predicted battery degradation
* Estimated remaining battery capacity
* Projected yearly energy costs
* Charging recommendations

Agents are encouraged to identify and implement additional derived metrics whenever they provide meaningful insights.

---

# Polling Strategy

Not every Tesla API should be called at the same frequency.

The platform should intelligently determine polling intervals based on:

* Vehicle state
* Charging status
* Driving status
* Sleep state
* Data volatility
* API rate limits
* Battery impact
* Cost

The vehicle should not be unnecessarily awakened solely for data collection.

Adaptive polling is preferred over fixed schedules.

---

# Storage Strategy

The persistence layer should support efficient historical analysis.

Prefer immutable event records.

Avoid designs that lose historical information.

Optimize for:

* Trend analysis
* Time-series queries
* Aggregations
* Forecasting
* Dashboards

---

# Dashboard Philosophy

Dashboards should explain the vehicle's behavior rather than merely displaying values.

Useful visualizations include:

Battery

* Battery health over time
* Capacity degradation
* Range degradation
* Battery temperature history

Charging

* Charging sessions
* Energy added
* Charging locations
* Charger type distribution
* Charging efficiency

Driving

* Daily distance
* Monthly distance
* Energy consumption
* Efficiency trends
* Energy per kilometer

Cost

* Electricity cost
* Cost per kilometer
* Monthly expenses
* Savings versus gasoline

Vehicle Usage

* Daily activity
* Idle time
* Phantom drain
* Vehicle availability

Forecasts

* Battery lifespan
* Expected degradation
* Annual charging costs
* Future range estimates

Agents are encouraged to propose new dashboards whenever historical data supports additional insights.

---

# AI Responsibilities

AI coding assistants should proactively:

* Suggest new metrics.
* Identify missing historical data.
* Recommend better data models.
* Detect opportunities for aggregation.
* Recommend useful dashboards.
* Identify performance optimizations.
* Suggest statistical analyses.
* Recommend forecasting models.
* Detect anomalies.
* Improve maintainability.

Agents should think like both a software architect and a data analyst.

---

# Success Criteria

The project succeeds when the owner can understand the long-term behavior of the vehicle without manually inspecting Tesla API responses.

Every feature should increase the owner's understanding of:

* Battery health
* Charging behavior
* Driving efficiency
* Vehicle utilization
* Operating costs
* Long-term trends
* Future predictions

The project should evolve into a comprehensive personal Tesla intelligence platform rather than a simple Tesla API client.

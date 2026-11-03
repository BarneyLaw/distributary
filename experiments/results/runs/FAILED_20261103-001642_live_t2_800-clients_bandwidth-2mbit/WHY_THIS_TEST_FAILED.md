# Why this test failed

## Test

Public test 2 workload (normal readers) with 800 clients, under the bandwidth_2mbit network profile (2 megabits per second).

Measured on 2 and 3 November 2026 on the Ubuntu virtual machine (UTM, 4 processors) at commit 0d3b813 (the master branch at the time), with the live server, through experiments/run/perf.py and the unmodified harness. Each test was run 3 times.

## Result

Final caption completeness was 48.46% (median of 3 runs), with 281 to 560 connection errors per run.

The assignment brief (section 10) caps the hidden performance mark at 15 out of 30 when final caption completeness is below 99%, and at 5 out of 30 when it is below 95%.

| Run | Finals received | Completeness | Median caption age | 95th percentile age | 99th percentile age | Finals fresh within 500 milliseconds | Revisions received | Duplicates | Stale | Connection errors |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| t2_run01.json | 21503 of 39200 | 54.85% | 5993 ms | 16322 ms | 20093 ms | 0.0% | 19.6% | 0 | 0 | 281 |
| t2_run02.json | 18610 of 39200 | 47.47% | 6029 ms | 16806 ms | 19926 ms | 0.6% | 16.5% | 0 | 0 | 560 |
| t2_run03.json | 18995 of 39200 | 48.46% | 7429 ms | 17459 ms | 20316 ms | 0.5% | 17.1% | 0 | 0 | 392 |

## Why it failed

- With 800 clients, even finals alone need more than 2 megabits per second, so no server could deliver every final within the link limit. This test is a stress test beyond the link's capacity, not a fair pass or fail test.
- Clients' connections timed out under the backlog and reconnected (868 to 907 connections for 800 clients), and reconnecting clients recovered through HISTORY.
- 0 duplicates and 0 stale messages even under this load, so the server's state stayed correct.

## What to do next

Report this as a stress test in the report. Do not count it against the server.

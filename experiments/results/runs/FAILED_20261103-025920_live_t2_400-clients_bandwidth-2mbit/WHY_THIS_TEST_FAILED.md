# Why this test failed

## Test

Public test 2 (scale) with 400 clients instead of 100, for 40 seconds, under the bandwidth_2mbit network profile (2 megabits per second shared by all loopback traffic).

Re-run on 3 November 2026 on the same virtual machine at commit 6408e06 (master after merging pull request 19, fanout-optimization), with harness/run_demo.py directly (3 runs, same test settings and profile). It has no row in perf_log.csv. This is a re-run of the earlier failed run of the same test, to check the fanout-optimization change.

## Result

Final caption completeness was 89.72% to 92.17%, below the 99% gate and below the 95% gate. The assignment brief (section 10) caps the hidden performance mark at 15 out of 30 below 99%, and at 5 out of 30 below 95%.

| Run | Finals received | Completeness | Median caption age | 95th percentile age | Finals fresh within 500 milliseconds | Revisions received | Connections | Connection errors | Duplicates | Stale |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| t2_run01.json | 17696 of 19600 | 90.29% | 1458 ms | 5881 ms | 0.0% | 48.7% | 412 | 12 | 0 | 0 |
| t2_run02.json | 17585 of 19600 | 89.72% | 1464 ms | 6839 ms | 0.0% | 48.0% | 411 | 11 | 0 | 0 |
| t2_run03.json | 18065 of 19600 | 92.17% | 1493 ms | 5319 ms | 0.0% | 48.8% | 412 | 12 | 0 | 0 |

## Why it failed

- Better than before the merge (82.89% to 84.15%), but the server still builds a backlog of several seconds (median caption age about 1,500 milliseconds, 95th percentile 5,300 to 6,800 milliseconds), and no final arrived within 500 milliseconds.
- Every revision to 400 clients does not fit in 2 megabits per second; finals alone probably do (rough estimate, about 1.1 to 1.4 megabits per second). So a server that switched to finals only early enough could do better, but this test is 4 times the public client count on the tightest profile and is unlikely to be a hidden test.
- 11 to 12 connection errors per run: the new 3-second reset closed some clients that fell behind, and they recovered through HISTORY.
- 0 duplicates, 0 stale messages.

## What to do next

Not a priority before the deadline. In degraded mode the server sends a ping after every frame and waits for the reply, which adds traffic on a link that is already full; that is the first place to look if the team wants to improve this result.

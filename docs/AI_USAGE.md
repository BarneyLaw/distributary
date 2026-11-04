# AI / Coding-Assistant Usage

## Tools used
- Claude
- ChatGPT (Codex)

## One useful suggestion
### Problem
Previously, a client is disconnected only under two terms: if it is asking caption older than the oldest final currently stored by the server or if it does not reply to the ping for hub.pongTimeout time. The server will never disconnect a slow client. Instead, it will keep the slow client in degraded mode regardless of how slow the client is, as long as it is still answering the pong within hub.pongTimeout time. This implementation causes our implementation's final caption completeness to fall below 99% on T3.
### Claude's Suggestion
Fix: reset hopelessly backlogged clients. When a pong comes back more than 3 s late, set SO_LINGER 0 and close the connection, which sends a TCP RST and throws away the stale data in the kernel buffers. The harness reconnects within 0.4 s, and HISTORY sends every missed final in one message, which a slow reader consumes with a single 500 ms sleep. This uses only the spec’s own reconnect path. It’s the “slow consumer” policy Redis and NATS use, and it matches spec section 10’s “converge quickly to the newest useful state instead of draining stale queued revisions”.
### Why it is useful
After implementing Claude's suggestion to disconnect client if it is too slow, our implementation's final caption completeness passes 99% again. This is because the send-history-first mechanism implemented for a new client will deliver the final caption quicker than the degraded-mechanism when facing a very slow client.

## One suggestion that was wrong, inefficient, or required modification
### Problem
After implementing backpressure, the system is tested using multipass on a shared folder. The result of the test show a significant drop in final caption completeness for T3 from 98% (before backpressure) to 80% (after backpressure). ChatGPT was asked to check if there are any problems with the backpressure implementation where it insisted that the problem is with the 60-second cutoff of the test case. It was aware that I run the code using multipass but it never flagged if the problem may be caused by multipass.
### ChatGPT's Suggestion
The main cause is still the 60-second cutoff. Your latest run contains the controller fix, but it still schedules 76 segments that take longer than 60 seconds to generate. Changing queueDelayTreshold cannot recover captions generated after the clients stop listening.
### My Correction
I flagged to ChatGPT that 60-second cutoff is not changeable as the previous implementation without backpressure achieved 98% within 60 seconds cutoff. I proceed with asking it that it might be a multipass issue after I found out that the 98% final caption completeness achieved before backpressure implementation was irreplicable on multipass. Only, then it started to run checks on multipass and established that it was a multipass issue.

## Responsibility statement
We understand that the team remains responsible for the submitted code, experimental results, and claims, and that every member may be asked to explain or modify relevant code during the viva.

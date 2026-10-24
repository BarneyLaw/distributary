### Stack

- Go 1.27.x
- Github actions for CI (dont measure performance)
- Local testing -> either VM or Native Ubuntu Linux
- Python interpreter 3.12 minimum
  

### Architecture

- Send by byte or by each english word?
- When client disconnects and joins back after their windows is totally out of the server's main window, do we just Fast forward and the client loses whatever doesnt overlap?

- We maintain a per client window/buffer on the server in additional to a master server window. An implementation with only the master server can solve reconnection, but maybe not slow readers.

- We need to define what is a slow client, SEQ - ACK > n

- We need to define what is considered total disconnect


### Details to Solve
1. Replay:
   - All clients' clock are sync to the server clock.
   - Clients replay from their own local buffer synced to server's clock.
2. Slow readers:
   - Use Producer-Consumer architecture with a bounded queue. Implemented as a time-series bucket pointing to a map with sequence number as the key.
   - If a consumer is too slow, the oldest information that has not been consumed may be replaced.
   - Silly proof:
        1. The queue is physically bounded in the memory.
        2. If the memory is full, producer will overwrite the oldest entry in the queue.
        3. Thus, the queue cannot go indefinitely.
3. Disconnect:
   - If totally out of windows with or without overlapping join, then consider late join
   - If server window start > cliend window end -> shutdown websocket
   - Disconnect is when no ack/req received for 20s
4. Late join: 
   - Start from earliest point of the Server's window
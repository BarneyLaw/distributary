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

- We need to synchronize the client's clock with the server. The authoritative timing will be based on the server clock

### Work distribution

- Please write a spec file in the component, detailing the intended implementation so that others can work along it rather than waiting.
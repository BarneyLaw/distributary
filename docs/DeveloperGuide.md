# DEVELOPER GUIDE

## Leifsen
### 25/10
 - Added a naive re-implementation of server in Go
 - Documented performance of server
 - Added the CI & Makefile for the repo
## Zheng Hao

## William

## Sabil
### 30/10
- Implement a naive independent client connections based on naive.go
- Create an early documentation for fanout

### Fanout
Fanout is a component that enforce one independent connection per-client and all of its policies.

#### Hub
Hub is the only gateway for other components to communicate with fanout. Hub is instantiated only through NewHub(). The following functions are usable by other components:<br>
* NewHub(session *Session) *hub
  - Instantiates a new hub that is dependent on the passed session.
  - All clients orchestrated by the returned hub will retrieve caption from the passed session.
  - The hub structure is not exported, thus all of its inner structure are non accessible from other components. This also prevents instantiation of hub outside of NewHub().
* (h *hub) AddClient(connection *websocket.Conn) error
  - Add a new client connection to hub.
  - Trying to add a client to a hub that has been shutted down will return an error.
  - AddClient will not return unless there is an error or the client connection is terminated.
* (h *hub) Shutdown()
  - When executed, all connections orchestrated by hub will be terminated.
  - After Shutdown(), the hub will no longer be usable. A NewHub() must be executed.
# Geobin Changelog

# 1.0.3
* fix visibility toggle bug
* zoom in on first new request
* zoom to extent of all requests when loading a bin

# 1.0.2
* refactor services
  * add `api.endpoint`
  * refactor `api.ws`
    * change `api.ws` to `api.ws.open`
    * change `api.close` to `api.ws.close`
    * expose `sockets` as `api.ws.sockets`
  * change `appVersion` to `clientVersion`
* Added build script. See the Cross-compiled Build section in the [server docs](static/doc/server.md)
* add analytics

# 1.0.1
* change example location

# 1.0.0
* initial release
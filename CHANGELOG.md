## numberly-v1.0.2 (2026-11-23)

### Fix

- **envoy**: match SNI with trailing dot to the host filter chain

## numberly-v1.0.1 (2026-11-16)

### Fix

- match vhosts when the Host header has a trailing dot

## numberly-v1.0.0 (2026-11-13)

### Feat

- add annotation to allow configuration on http duration + fix de… (#16)
- add support for sticky sessions (#14)
- update README
- adding mTLS support on envoy upstream
- adding mTLS downstream configuration + some tests
- update readme
- **aggregator.go**: adding a warn log when err is not nil
- **mode-maintenance**: handle mode maintenance for clusters
- **split-timeout**: Fix tests
- **split-timeout**: fix comment and default TO for cluster
- **split-timeout**: Default Timeouts flags and config
- **split-timeout**: Docs
- **split-time**: splits timeout annotation into 3 different ones
- **weight-annotation**: Annotation to add load-balancing weight
- **split-timeout**: Fix tests
- **split-timeout**: fix comment and default TO for cluster
- **split-timeout**: Default Timeouts flags and config
- **split-timeout**: Docs
- **split-time**: splits timeout annotation into 3 different ones
- **weight-annotation**: Annotation to add load-balancing weight

### Fix

- drop SHA-1 from downstream TLS signature algorithms
- warn when dnsRefreshRate is not configured
- reject 1ms dns-refresh-rate and leave zero unset
- fail closed for mTLS
- pr
- **file**: remove unwanted file
- **metrics**: be aware when an ingress is deleted and remove it correctly from the metrics
- pkg/envoy was wrongly targeted by the gitignore

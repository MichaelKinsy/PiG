### Fixed

- A stream header or metadata value changed in place on the settings object a Durable `Harness` returns from its settings callback now reaches the core with the next event, as it does in Pi, which re-reads the settings at every use. The core kept the settings it had encoded last when the new value compared equal to a stored copy that shared the same map. <!-- fix: 776588b309 -->

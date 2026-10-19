### Fixed

- PiG no longer hangs for 30 seconds at exit when two extensions share a Node process and one is still waiting on a host call (seen with a global project-trust extension and a project extension).

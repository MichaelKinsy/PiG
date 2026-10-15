# SPDX-License-Identifier: MIT
"""Runs bench/evals' pigbench from an agent bundle or the benchmark image: `python3.12 pigbench.py <bench> ...`.

The bundle keeps pigbench under evals/ beside this file, as the repository keeps it under bench/evals/.
"""
import os
import runpy
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "evals"))
runpy.run_module("pigbench", run_name="__main__", alter_sys=True)

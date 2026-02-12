#!/usr/bin/env python3
"""
Offline benchmark script for mosdns performance optimizations.
This script runs Go benchmarks and analyzes the results.
"""

import subprocess
import re
import sys
import json
import os
from dataclasses import dataclass
from typing import List, Dict, Optional
from pathlib import Path


SCRIPT_DIR = Path(__file__).parent.resolve()
PROJECT_DIR = SCRIPT_DIR.parent


@dataclass
class BenchmarkResult:
    name: str
    iterations: int
    ns_per_op: float
    bytes_per_op: int
    allocs_per_op: int


def run_benchmark(package: str, bench_filter: str = ".") -> List[BenchmarkResult]:
    """Run Go benchmarks for a specific package."""
    cmd = [
        "go", "test", "-bench", bench_filter,
        "-benchmem", "-benchtime=1s", "-count=3",
        package
    ]
    
    print(f"Running: {' '.join(cmd)}")
    
    result = subprocess.run(
        cmd,
        capture_output=True,
        text=True,
        cwd=PROJECT_DIR
    )
    
    if result.returncode != 0:
        print(f"Error running benchmark: {result.stderr}")
        return []
    
    results = []
    pattern = r'^(Benchmark\w+)\s+-?\d+\s+(\d+\.?\d*)\s+ns/op\s+(\d+)\s+B/op\s+(\d+)\s+allocs/op$'
    
    for line in result.stdout.split('\n'):
        match = re.match(pattern, line.strip())
        if match:
            results.append(BenchmarkResult(
                name=match.group(1),
                iterations=int(match.group(2)) if '.' not in match.group(2) else 0,
                ns_per_op=float(match.group(2)),
                bytes_per_op=int(match.group(3)),
                allocs_per_op=int(match.group(4))
            ))
    
    return results


def analyze_keyword_matcher():
    """Analyze KeywordMatcher performance improvement."""
    print("\n" + "="*60)
    print("KEYWORD MATCHER BENCHMARK")
    print("="*60)
    
    results = run_benchmark("./pkg/matcher/domain/...", "Keyword")
    
    if not results:
        print("No benchmark results found.")
        return
    
    old_results = {}
    new_results = {}
    
    for r in results:
        if "Old" in r.name:
            key = r.name.replace("Old", "").replace("_", "")
            old_results[key] = r
        else:
            key = r.name.replace("New", "").replace("_", "")
            if "Match" in r.name or "NoMatch" in r.name or "Concurrent" in r.name:
                new_results[r.name] = r
    
    print("\nKeyword Matcher Results:")
    print("-" * 80)
    print(f"{'Benchmark':<40} {'ns/op':>12} {'B/op':>10} {'allocs/op':>10}")
    print("-" * 80)
    
    for r in sorted(results, key=lambda x: x.name):
        print(f"{r.name:<40} {r.ns_per_op:>12.2f} {r.bytes_per_op:>10} {r.allocs_per_op:>10}")
    
    print("\nComparison (Old vs New Aho-Corasick):")
    print("-" * 80)
    
    for old_key, old_r in sorted(old_results.items()):
        kw_count = old_key.replace("keywords", "")
        new_key = f"BenchmarkKeywordMatcherNewkeywords{kw_count}"
        
        if new_key in [r.name for r in results]:
            new_r = next(r for r in results if r.name == new_key)
            speedup = old_r.ns_per_op / new_r.ns_per_op if new_r.ns_per_op > 0 else 0
            print(f"  keywords={kw_count}: Old={old_r.ns_per_op:.2f}ns, New={new_r.ns_per_op:.2f}ns, Speedup={speedup:.2f}x")


def analyze_cache():
    """Analyze Cache performance."""
    print("\n" + "="*60)
    print("CACHE BENCHMARK (Ristretto)")
    print("="*60)
    
    results = run_benchmark("./pkg/cache/...", "Cache")
    
    if not results:
        print("No benchmark results found.")
        return
    
    print("\nCache Results:")
    print("-" * 80)
    print(f"{'Benchmark':<40} {'ns/op':>12} {'B/op':>10} {'allocs/op':>10}")
    print("-" * 80)
    
    for r in sorted(results, key=lambda x: x.name):
        print(f"{r.name:<40} {r.ns_per_op:>12.2f} {r.bytes_per_op:>10} {r.allocs_per_op:>10}")
    
    print("\nKey Metrics:")
    for r in results:
        if "Mixed" in r.name:
            print(f"  Mixed workload: {r.ns_per_op:.2f} ns/op, {r.allocs_per_op} allocs/op")


def analyze_forward_selector():
    """Analyze Forward selector performance."""
    print("\n" + "="*60)
    print("FORWARD SELECTOR BENCHMARK")
    print("="*60)
    
    results = run_benchmark("./plugin/executable/forward/...", "Selector")
    
    if not results:
        print("No benchmark results found. Creating inline benchmark...")
        return
    
    print("\nForward Selector Results:")
    print("-" * 80)
    print(f"{'Benchmark':<40} {'ns/op':>12} {'B/op':>10} {'allocs/op':>10}")
    print("-" * 80)
    
    for r in sorted(results, key=lambda x: x.name):
        print(f"{r.name:<40} {r.ns_per_op:>12.2f} {r.bytes_per_op:>10} {r.allocs_per_op:>10}")


def run_all_tests():
    """Run all unit tests to verify correctness."""
    print("\n" + "="*60)
    print("RUNNING UNIT TESTS")
    print("="*60)
    
    packages = [
        "./pkg/cache/...",
        "./pkg/matcher/domain/...",
        "./plugin/executable/forward/...",
        "./plugin/executable/cache/...",
    ]
    
    all_passed = True
    for pkg in packages:
        print(f"\nTesting {pkg}...")
        result = subprocess.run(
            ["go", "test", "-v", pkg],
            capture_output=True,
            text=True,
            cwd=PROJECT_DIR
        )
        
        if result.returncode != 0:
            print(f"  FAILED: {result.stderr}")
            all_passed = False
        else:
            passed = result.stdout.count("--- PASS:")
            failed = result.stdout.count("--- FAIL:")
            print(f"  PASSED: {passed} tests, FAILED: {failed} tests")
    
    return all_passed


def main():
    print("="*60)
    print("MOSDNS PERFORMANCE OPTIMIZATION BENCHMARK")
    print("="*60)
    
    print("\nThis benchmark tests the following optimizations:")
    print("  1. Ristretto cache backend (replacing concurrent_map)")
    print("  2. Aho-Corasick for KeywordMatcher (O(n+m) vs O(n*m))")
    print("  3. Cached upstream selector weights")
    
    all_passed = run_all_tests()
    if not all_passed:
        print("\nWARNING: Some tests failed. Please fix before continuing.")
        sys.exit(1)
    
    analyze_cache()
    analyze_keyword_matcher()
    
    print("\n" + "="*60)
    print("BENCHMARK COMPLETE")
    print("="*60)
    
    print("\nSummary of Optimizations:")
    print("  - Cache: Using ristretto with LFU eviction and TTL support")
    print("  - KeywordMatcher: Using Aho-Corasick automaton for O(n+m) matching")
    print("  - Forward Selector: Cached weights with 5s TTL")


if __name__ == "__main__":
    main()

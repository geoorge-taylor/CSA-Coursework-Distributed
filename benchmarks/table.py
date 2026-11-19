"""Turn BenchmarkDistributed output into one results table per image size.

Usage:
    python3 benchmarks/table.py benchmarks/results.txt

Prints a Markdown table and a LaTeX (booktabs) table for each size. Time is the
mean over every -count run; speedup is relative to 1 worker with 1 thread on
the same image size.
"""

import re
import statistics
import sys
from collections import defaultdict

# e.g. "BenchmarkDistributed/1024x1024/workers=2/threads=4-8   1   123456789 ns/op"
LINE = re.compile(
    r"^BenchmarkDistributed/(\d+)x\d+/workers=(\d+)/threads=(\d+)(?:-\d+)?\s+\d+\s+([\d.]+) ns/op"
)
TRANSFER = re.compile(r"^BenchmarkDistributed/(\d+)x\d+/transfer-only(?:-\d+)?\s+\d+\s+([\d.]+) ns/op")


def main():
    if len(sys.argv) != 2:
        sys.exit("usage: python3 benchmarks/table.py <results.txt>")

    # {size: {(workers, threads): [seconds per run]}}
    times = defaultdict(lambda: defaultdict(list))
    with open(sys.argv[1]) as f:
        for line in f:
            m = LINE.match(line.strip())
            if m:
                size, workers, threads = int(m.group(1)), int(m.group(2)), int(m.group(3))
                times[size][(workers, threads)].append(float(m.group(4)) / 1e9)

    if not times:
        sys.exit("no BenchmarkDistributed lines found")

    # 0-turn runs (AWS mode): the time to send the board to the broker and back
    transfer = defaultdict(list)
    with open(sys.argv[1]) as f:
        for line in f:
            m = TRANSFER.match(line.strip())
            if m:
                transfer[int(m.group(1))].append(float(m.group(2)) / 1e9)

    for size in sorted(times):
        configs = times[size]
        transfer_mean = statistics.mean(transfer[size]) if transfer[size] else 0.0
        # with a transfer measurement, time and speedup are for processing only
        base = configs.get((1, 1)) or configs[min(configs)]
        base_mean = statistics.mean(base) - transfer_mean
        runs = len(next(iter(configs.values())))

        rows = []
        for workers, threads in sorted(configs):
            values = configs[(workers, threads)]
            mean = statistics.mean(values) - transfer_mean
            std = statistics.stdev(values) if len(values) > 1 else 0.0
            rows.append((workers, threads, mean, std, base_mean / mean))

        print(f"\n{size}x{size}, 100 turns, mean of {runs} runs, speedup vs 1 worker / 1 thread")
        if transfer_mean:
            print(f"times exclude {transfer_mean:.3f} s of sending the board to the broker and back")
        print()
        print("| Workers | Threads per worker | Time (s) | Speedup |")
        print("|--:|--:|--:|--:|")
        for workers, threads, mean, std, speedup in rows:
            print(f"| {workers} | {threads} | {mean:.3f} ± {std:.3f} | {speedup:.2f} |")

        print("\n\\begin{tabular}{rrrr}")
        print("\\toprule")
        print("Workers & Threads per worker & Time (s) & Speedup \\\\")
        print("\\midrule")
        for workers, threads, mean, _, speedup in rows:
            print(f"{workers} & {threads} & {mean:.3f} & {speedup:.2f} \\\\")
        print("\\bottomrule")
        print("\\end{tabular}")


if __name__ == "__main__":
    main()

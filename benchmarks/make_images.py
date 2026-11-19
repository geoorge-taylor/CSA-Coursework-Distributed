"""Generate larger random Game of Life boards for benchmarking.

Usage:
    python3 benchmarks/make_images.py

Writes images/768x768.pgm and images/1024x1024.pgm in the same binary PGM
format as the provided images (0 = dead, 255 = alive). A fixed seed means the
boards are identical every time you run it.
"""

import os
import random

SIZES = [768, 1024]
ALIVE_PROBABILITY = 0.25
SEED = 2026


def main():
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    rng = random.Random(SEED)
    for size in SIZES:
        cells = bytes(255 if rng.random() < ALIVE_PROBABILITY else 0 for _ in range(size * size))
        path = os.path.join(root, "images", f"{size}x{size}.pgm")
        with open(path, "wb") as f:
            f.write(f"P5\n{size} {size}\n255\n".encode())
            f.write(cells)
        print("wrote", path)


if __name__ == "__main__":
    main()

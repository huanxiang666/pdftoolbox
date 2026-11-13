"""PyInstaller launcher for the CLI executable (console)."""
import multiprocessing
import sys

if __name__ == "__main__":
    # freeze_support MUST run before importing pdftoolbox.* : the office
    # bridge spawns worker processes, and a spawned child re-imports this
    # launcher — freeze_support makes it become a worker instead of
    # re-running the CLI from scratch.
    multiprocessing.freeze_support()
    from pdftoolbox.cli import main

    sys.exit(main())

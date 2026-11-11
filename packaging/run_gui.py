"""PyInstaller launcher for the GUI executable (windowed, no console)."""
import sys

from pdftoolbox.gui import main

if __name__ == "__main__":
    sys.exit(main())

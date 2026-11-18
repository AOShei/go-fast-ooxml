"""Run one comparison tool on one file and print the result as JSON.

Usage: oracle.py <tool> <file>

Called by internal/ooxmltest.Python. A tool that fails on the file is a
result, not an error: the JSON then has "error" set and the exit status is 0.
"""
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import tools  # noqa: E402


def main():
    if len(sys.argv) != 3 or sys.argv[1] not in tools.TOOLS:
        sys.exit(f"usage: oracle.py <{'|'.join(tools.TOOLS)}> <file>")
    tool, path = sys.argv[1], sys.argv[2]
    result = {"tool": tool, "version": tools.tool_version(tool), "text": ""}
    try:
        result["text"] = tools.TOOLS[tool][1](path)
    except Exception as e:  # the tool's failure is the finding
        result["error"] = f"{type(e).__name__}: {e}"
    json.dump(result, sys.stdout, ensure_ascii=False)


if __name__ == "__main__":
    main()

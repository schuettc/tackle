import sys

if sys.version_info >= (3, 9):
    GUARD = 1
    FIRST = "first"
else:
    GUARD = 2
    FIRST = "second"

try:
    import json
    TRIED = 1

    def tried_fn():
        return 1
except ImportError:
    TRIED = 2
finally:
    FINAL = 3

with open(__file__) as _f:
    class Ctx:
        pass

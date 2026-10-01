import enum

SETTING = (2022, 2023, 2024)
TABLE = {"a": 1}
BUILT = dict(a=1)


class Cause:
    kind = "x"


class Mode(enum.Enum):
    X = 1


def helper():
    return 1

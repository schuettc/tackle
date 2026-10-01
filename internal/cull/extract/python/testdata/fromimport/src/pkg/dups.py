def twice():
    return 1


def twice():
    return 2


try:
    def both():
        return "nested"
except ImportError:
    pass


def both():
    return "top"

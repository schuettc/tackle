import pytest

from pkg.calc import add, multiply


def helper_value():
    return 41


def test_add():
    assert add(2, 3) == 5


def test_add_with_helper():
    assert add(helper_value(), 1) == 42


def test_fixture_use(calculator):
    assert calculator["value"] == 0


@pytest.mark.parametrize("a,b,expected", [(1, 2, 3), (2, 2, 4)])
def test_add_parametrized(a, b, expected):
    assert add(a, b) == expected


class TestCalculator:
    def test_multiply(self):
        assert multiply(2, 3) == 6

    def test_multiply_zero(self):
        assert multiply(0, 5) == 0

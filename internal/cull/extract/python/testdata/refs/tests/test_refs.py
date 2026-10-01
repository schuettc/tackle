import pkg.consts
from pkg.consts import HONEST_SEASONS, LIMIT, Cause, helper


def test_const():
    assert HONEST_SEASONS == (2022, 2023, 2024)


def test_class():
    assert hasattr(Cause, "x")
    assert isinstance(object(), Cause)


def test_inner_import():
    from pkg.consts import OTHER

    assert OTHER == 1


def test_order():
    assert LIMIT == 10
    assert pkg.consts.OTHER == 1
    assert helper() == 1
    assert HONEST_SEASONS

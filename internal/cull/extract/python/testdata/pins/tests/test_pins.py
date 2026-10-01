import pkg.consts
import pytest
from pkg.consts import BIG, BUILT, SETTING, TABLE, Cause, Mode, helper


def local_helper():
    return 1


@pytest.fixture
def thing():
    return 1


def test_const():
    assert SETTING == (2022, 2023, 2024)


def test_hasattr():
    assert hasattr(Cause, "kind")


def test_enum():
    assert isinstance(Mode.X, Mode)


def test_method_on_constant():
    assert sorted(TABLE.keys()) == ["a"]


def test_module_attribute():
    assert pkg.consts.SETTING[0] == 2022


def test_builtin_fixture(tmp_path, monkeypatch):
    assert SETTING and tmp_path


def test_calls_project_function():
    assert helper() == SETTING[0]


def test_constructs_project_class():
    assert Cause().kind == "x"


def test_calls_through_module():
    assert pkg.consts.helper() == SETTING[0]


def test_method_on_constructed_constant():
    assert BUILT.get("a") == 1


def test_same_file_helper():
    assert local_helper() == SETTING[0]


def test_fixture_parameter(thing):
    assert SETTING and thing


def test_no_code_under_test():
    assert 1 + 1 == 2


LOCAL_SETTING = frozenset()


def test_setting_in_test_file():
    assert LOCAL_SETTING == frozenset()


def test_big_setting_over_budget():
    assert len(BIG) == 40


def test_module_attribute_read():
    assert hasattr(pkg, "consts")


def test_literals_only():
    assert len([1, 2]) == 2


ANNOTATED: frozenset = frozenset()


def test_annotated_setting_in_test_file():
    assert ANNOTATED == frozenset()

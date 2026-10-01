from pkg import loaders as ld
from pkg import guarded


def test_alias_load():
    assert ld.LOADERS


def test_alias_call():
    assert ld.load() == 1


def test_inner():
    from pkg import loaders

    assert loaders.LOADERS


def test_guarded():
    assert guarded.GUARD
    assert guarded.FIRST
    assert guarded.TRIED
    assert guarded.FINAL
    assert guarded.Ctx
    assert guarded.tried_fn()

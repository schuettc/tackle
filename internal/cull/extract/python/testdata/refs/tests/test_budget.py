from pkg.big import BIG, SMALL, big_call


def test_skips_big_reference():
    assert BIG != SMALL


def test_big_call_target_truncates():
    assert big_call()

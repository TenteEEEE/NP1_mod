import importlib.util
import pathlib
import unittest


location = pathlib.Path(__file__).resolve().parents[1] / 'tools' / 'remote' / 'usb-uplink-fixture.py'
spec = importlib.util.spec_from_file_location('usb_uplink_fixture', location)
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)


class UplinkFixtureTests(unittest.TestCase):
    def test_only_expected_internal_router_advertisements_are_ignored(self):
        before = {'routes': [{'dev': 'wlo1', 'protocol': 'dhcp', 'dst': 'default'}],
                  'routes6': [], 'profiles': {'baseline': 'same'}}
        internal_ra = {'dev': 'wlo1', 'protocol': 'ra', 'dst': 'default'}
        current = {**before, 'routes6': [internal_ra]}
        self.assertEqual(fixture.snapshot_changes(before, current, ['wlo1', 'wlx000000000002']), [])
        current['routes6'] = [{**internal_ra, 'dev': 'ncct00000001'}]
        self.assertEqual(fixture.snapshot_changes(before, current, ['wlo1']), ['routes6'])
        current['routes6'] = [internal_ra]
        current['routes'] = []
        current['profiles'] = {'baseline': 'changed'}
        self.assertEqual(fixture.snapshot_changes(before, current, ['wlo1']), ['routes', 'profiles'])

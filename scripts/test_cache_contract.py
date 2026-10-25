import dataclasses
import unittest

from cache_contract import Authorization, Entry, Identity, exact_key, inspect_reuse, invalidation_closure


class CacheAdmissionContractTests(unittest.TestCase):
    def setUp(self):
        self.identity = Identity("tenant-a", "case-a", "artifact-a", "parsed-a", "rules-a",
                                 "revision-a", "epoch-a", "dependencies-a")
        self.authorization = Authorization("tenant-a", "case-a", "epoch-a", True)
        self.entry = Entry(self.identity, "content-digest", "content-digest")
        self.secret = b"synthetic-contract-example-only"

    def test_denied_or_wrong_scope_never_observes_cache_existence(self):
        for authorization in [dataclasses.replace(self.authorization, allowed=False),
                              dataclasses.replace(self.authorization, tenant="tenant-b"),
                              dataclasses.replace(self.authorization, case="case-b"),
                              dataclasses.replace(self.authorization, epoch="revoked")]:
            with self.subTest(authorization=authorization):
                reads = []
                result = inspect_reuse(self.identity, lambda: authorization,
                                       lambda key: reads.append(key), self.secret, immutable_gate=True)
                self.assertEqual(result, "denied")
                self.assertEqual(reads, [])

    def test_unpassed_immutable_gate_prevents_lookup_even_when_authorized(self):
        reads = []
        result = inspect_reuse(self.identity, lambda: self.authorization,
                               lambda key: reads.append(key), self.secret)
        self.assertEqual(result, "bypass_immutable_gate")
        self.assertEqual(reads, [])

    def test_revocation_between_lookup_and_return_refuses_reference(self):
        authority = iter([self.authorization, dataclasses.replace(self.authorization, allowed=False)])
        reads = []
        def lookup(key):
            reads.append(key)
            return self.entry
        result = inspect_reuse(self.identity, lambda: next(authority), lookup,
                               self.secret, immutable_gate=True)
        self.assertEqual(result, "denied")
        self.assertEqual(reads, [exact_key(self.identity, self.secret)])

    def test_each_identity_change_partitions_keys_and_rejects_stale_entry(self):
        for field in dataclasses.fields(self.identity):
            with self.subTest(field=field.name):
                stale = dataclasses.replace(self.identity, **{field.name: "changed-version"})
                self.assertNotEqual(exact_key(stale, self.secret), exact_key(self.identity, self.secret))
                result = inspect_reuse(self.identity, lambda: self.authorization,
                                       lambda _: dataclasses.replace(self.entry, identity=stale),
                                       self.secret, immutable_gate=True)
                self.assertEqual(result, "identity_mismatch")

    def test_partial_or_corrupt_artifact_cannot_become_hit(self):
        for entry in [dataclasses.replace(self.entry, state="partial"),
                      dataclasses.replace(self.entry, state="legacy_unfrozen"),
                      dataclasses.replace(self.entry, artifact_digest="corrupt"),
                      dataclasses.replace(self.entry, artifact_digest="", expected_digest="")]:
            with self.subTest(entry=entry):
                self.assertEqual(inspect_reuse(self.identity, lambda: self.authorization, lambda _: entry,
                                              self.secret, immutable_gate=True), "identity_mismatch")
        self.assertEqual(inspect_reuse(self.identity, lambda: self.authorization, lambda _: None,
                                      self.secret, immutable_gate=True), "miss")
        self.assertEqual(inspect_reuse(self.identity, lambda: self.authorization, lambda _: self.entry,
                                      self.secret, immutable_gate=True), "hit_reference_only")

    def test_unknown_revision_or_missing_gateway_key_is_not_a_valid_key(self):
        with self.assertRaises(ValueError):
            exact_key(dataclasses.replace(self.identity, model_revision=""), self.secret)
        with self.assertRaises(ValueError):
            exact_key(self.identity, b"")

    def test_global_definition_invalidation_reaches_fact_rule_finding_and_report(self):
        nodes = {"definition", "clause", "fact", "rule", "finding", "report", "independent"}
        edges = [("fact", "definition"), ("fact", "clause"), ("rule", "fact"),
                 ("finding", "rule"), ("report", "finding")]
        mode, affected = invalidation_closure(nodes, edges, {"definition"})
        self.assertEqual(mode, "incremental")
        self.assertEqual(affected, {"definition", "fact", "rule", "finding", "report"})
        for kwargs, changed, dependencies in [({"deleted": True}, {"clause"}, edges),
                                             ({"complete": False}, {"fact"}, edges),
                                             ({}, {"unknown-source"}, edges),
                                             ({}, {"fact"}, edges + [("rule", "unknown-source")]),
                                             ({}, {"fact"}, edges + [("definition", "report")])]:
            with self.subTest(kwargs=kwargs, changed=changed, dependencies=dependencies):
                self.assertEqual(invalidation_closure(nodes, dependencies, changed, **kwargs),
                                 ("full", nodes))


if __name__ == "__main__":
    unittest.main()

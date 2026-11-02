import unittest

from metadata_core import (
    MetadataError,
    STYLES,
    content_changed,
    insert_header,
    metadata_body,
    parse_header,
)


RECORD = {
    "purpose": "Keeps a concise example configuration for the test tool.",
    "authors": [{"name": "Bai Junhao", "email": "baijunhao@cq.chinamobile.com", "contact": "+86 13617696321"}],
    "created": {"value": "2026-01-02"},
    "updated": {"value": "2026-03-04"},
    "revision": "r1",
    "copyright": [{"notice": "See LICENSE"}],
}


class HeaderByteSafetyTests(unittest.TestCase):
    def assert_round_trip(self, body, style, kind):
        updated = insert_header(body, RECORD, style=style, handling="header-and-manifest", file_kind=kind, mode="100644")
        self.assertEqual(metadata_body(updated, style, "header-and-manifest", file_kind=kind, mode="100644"), body)
        self.assertFalse(content_changed(body, updated, mode_before="100644", mode_after="100644", style=style, handling="header-and-manifest", file_kind=kind))
        self.assertTrue(content_changed(body, updated, mode_before="100644", mode_after="100755", style=style, handling="header-and-manifest", file_kind=kind))
        self.assertEqual(insert_header(updated, RECORD, style=style, handling="header-and-manifest", file_kind=kind, mode="100644"), updated)

    def test_shebang_and_python_encoding_remain_before_header(self):
        body = b"#!/usr/bin/env python3\n# -*- coding: utf-8 -*-\nprint('ok')\n"
        updated = insert_header(body, RECORD, style=STYLES["hash"], handling="header-and-manifest", file_kind="python", mode="100644")
        self.assertTrue(updated.startswith(b"#!/usr/bin/env python3\n# -*- coding: utf-8 -*-\n# File metadata"))
        self.assert_round_trip(body, STYLES["hash"], "python")

    def test_go_build_constraints_remain_before_header(self):
        body = b"//go:build linux\n// +build linux\n\npackage example\n"
        updated = insert_header(body, RECORD, style=STYLES["slash"], handling="header-and-manifest", file_kind="go", mode="100644")
        self.assertTrue(updated.startswith(b"//go:build linux\n// +build linux\n\n// File metadata"))
        self.assert_round_trip(body, STYLES["slash"], "go")

    def test_markdown_frontmatter_and_crlf_round_trip(self):
        body = b"---\r\ntitle: sample\r\n---\r\n\r\n# Body\r\n"
        updated = insert_header(body, RECORD, style=STYLES["html"], handling="header-and-manifest", file_kind="markdown", mode="100644")
        self.assertTrue(updated.startswith(b"---\r\ntitle: sample\r\n---\r\n\r\n<!-- File metadata"))
        self.assert_round_trip(body, STYLES["html"], "markdown")

    def test_css_charset_round_trip(self):
        body = b"@charset \"UTF-8\";\nbody { color: black; }\n"
        updated = insert_header(body, RECORD, style=STYLES["block"], handling="header-and-manifest", file_kind="css", mode="100644")
        self.assertTrue(updated.startswith(b"@charset \"UTF-8\";\n/* File metadata"))
        self.assert_round_trip(body, STYLES["block"], "css")

    def test_header_only_edit_is_not_body_change_but_body_is(self):
        body = b"# executable body\n"
        first = insert_header(body, RECORD, style=STYLES["hash"], handling="header-and-manifest", file_kind="shell", mode="100755")
        second_record = {**RECORD, "updated": {"value": "unknown"}}
        second = insert_header(body, second_record, style=STYLES["hash"], handling="header-and-manifest", file_kind="shell", mode="100755")
        self.assertFalse(content_changed(first, second, mode_before="100755", mode_after="100755", style=STYLES["hash"], handling="header-and-manifest", file_kind="shell"))
        changed_body = second.replace(b"executable body", b"different body")
        self.assertTrue(content_changed(second, changed_body, mode_before="100755", mode_after="100755", style=STYLES["hash"], handling="header-and-manifest", file_kind="shell"))

    def test_manifest_only_and_protected_inputs_cannot_be_edited(self):
        raw_json = b'{"note":"File metadata (manifest mirror):"}\n'
        self.assertEqual(metadata_body(raw_json, STYLES["hash"], "manifest-only", mode="100644"), raw_json)
        for kind in ("json", "sql", "lock", "generated", "fixture", "third-party"):
            with self.subTest(kind=kind), self.assertRaises(MetadataError):
                insert_header(b"payload\n", RECORD, style=STYLES["hash"], handling="header-and-manifest", file_kind=kind, mode="100644")
        with self.assertRaises(MetadataError):
            insert_header(b"payload\n", RECORD, style=STYLES["hash"], handling="generated-manifest-only", file_kind="python", mode="100644")

    def test_malformed_duplicated_and_out_of_order_headers_fail_closed(self):
        valid = insert_header(b"body\n", RECORD, style=STYLES["hash"], handling="header-and-manifest", file_kind="shell", mode="100644")
        duplicated = valid + valid
        with self.assertRaises(MetadataError):
            parse_header(duplicated, STYLES["hash"])
        malformed = valid.replace(b"# Revision:", b"# Contact:")
        with self.assertRaises(MetadataError):
            metadata_body(malformed, STYLES["hash"], "header-and-manifest", file_kind="shell", mode="100644")
        missing_separator = valid.replace(b"End file metadata.\n\n", b"End file metadata.\n")
        with self.assertRaises(MetadataError):
            metadata_body(missing_separator, STYLES["hash"], "header-and-manifest", file_kind="shell", mode="100644")

    def test_existing_block_with_internal_comment_terminator_is_not_stripped(self):
        css = insert_header(b"body { color: black; }\n", RECORD, style=STYLES["block"], handling="header-and-manifest", file_kind="css", mode="100644")
        css_attack = css.replace(
            b"/* Purpose: " + RECORD["purpose"].encode() + b" */",
            b"/* Purpose: x */ alert(1); /* */",
        )
        with self.assertRaises(MetadataError):
            metadata_body(css_attack, STYLES["block"], "header-and-manifest", file_kind="css", mode="100644")

        html = insert_header(b"<main>safe</main>\n", RECORD, style=STYLES["html"], handling="header-and-manifest", file_kind="html", mode="100644")
        html_attack = html.replace(
            b"<!-- Purpose: " + RECORD["purpose"].encode() + b" -->",
            b"<!-- Purpose: x --> <script>bad()</script> <!-- -->",
        )
        with self.assertRaises(MetadataError):
            metadata_body(html_attack, STYLES["html"], "header-and-manifest", file_kind="html", mode="100644")

    def test_comment_style_must_match_supported_native_kind(self):
        for kind, style in (("python", STYLES["slash"]), ("go", STYLES["hash"]), ("typescript", STYLES["html"]), ("unrecognized", STYLES["hash"])):
            with self.subTest(kind=kind), self.assertRaises(MetadataError):
                insert_header(b"body\n", RECORD, style=style, handling="header-and-manifest", file_kind=kind, mode="100644")

    def test_nonregular_git_modes_never_normalize_header_bytes(self):
        body = b"body\n"
        with_header = insert_header(body, RECORD, style=STYLES["hash"], handling="header-and-manifest", file_kind="shell", mode="100644")
        for mode in ("120000", "160000", "777777"):
            with self.subTest(mode=mode):
                self.assertTrue(content_changed(body, with_header, mode_before=mode, mode_after=mode, style=STYLES["hash"], handling="header-and-manifest", file_kind="shell"))
                with self.assertRaises(MetadataError):
                    metadata_body(with_header, STYLES["hash"], "header-and-manifest", file_kind="shell", mode=mode)

    def test_header_in_body_position_fails_closed(self):
        valid = insert_header(b"body\n", RECORD, style=STYLES["hash"], handling="header-and-manifest", file_kind="shell", mode="100644")
        marker_end = valid.index(b"# End file metadata.") + len(b"# End file metadata.")
        misplaced = b"body\n" + valid[:marker_end] + b"\n\n"
        with self.assertRaises(MetadataError):
            metadata_body(misplaced, STYLES["hash"], "header-and-manifest", file_kind="shell", mode="100644")

    def test_unterminated_prologue_and_unknown_kind_refuse_insertion(self):
        with self.assertRaises(MetadataError):
            insert_header(b"#!/bin/sh", RECORD, style=STYLES["hash"], handling="header-and-manifest", file_kind="shell", mode="100644")
        with self.assertRaises(MetadataError):
            insert_header(b"body\n", RECORD, style=STYLES["hash"], handling="header-and-manifest", file_kind="unknown", mode="100644")


if __name__ == "__main__":
    unittest.main()

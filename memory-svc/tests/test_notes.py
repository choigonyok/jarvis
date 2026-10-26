import unittest

from app.graph import EXTRACTION_NOTE, OWNER, SELF_NAME


class ExtractionNoteTest(unittest.TestCase):
    def test_me_is_whose_graph_it_is(self):
        """'나' is the operator in the owner graph and the guest in the guest's -
        never the operator there, or the guest's taste lands on the operator."""
        self.assertIn(OWNER, EXTRACTION_NOTE["owner"])
        self.assertIn(SELF_NAME["guest"], EXTRACTION_NOTE["guest"])
        self.assertNotIn(OWNER, EXTRACTION_NOTE["guest"])
        self.assertEqual(set(EXTRACTION_NOTE), set(SELF_NAME))


if __name__ == "__main__":
    unittest.main()

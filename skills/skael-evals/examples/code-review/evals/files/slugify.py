"""Turn titles into URL slugs."""

import re
import unicodedata

_NON_WORD = re.compile(r"[^a-z0-9]+")


def slugify(title: str, max_length: int = 60) -> str:
    """Return a lowercase, hyphen-separated slug of at most max_length characters."""
    normalized = unicodedata.normalize("NFKD", title).encode("ascii", "ignore").decode("ascii")
    slug = _NON_WORD.sub("-", normalized.lower()).strip("-")
    return slug[:max_length].rstrip("-")

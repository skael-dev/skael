/// Returns the items on page `n` (1-based) of `size` items per page.
pub fn page(items: &[String], n: usize, size: usize) -> &[String] {
    let start = (n - 1) * size;
    let mut end = start + size;
    if start >= items.len() {
        return &[];
    }
    if end > items.len() {
        end = items.len();
    }
    &items[start..end + 1]
}

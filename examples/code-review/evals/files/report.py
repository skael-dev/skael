def monthly_report(rows):
    totals = {}
    for row in rows:
        month = row["date"][:7]
        if month not in totals:
            totals[month] = 0
        totals[month] = totals[month] + row["amount"]
    result = []
    for month in sorted(totals.keys()):
        result.append({"month": month, "total": totals[month]})
    return result

import sqlite3


def get_db():
    return sqlite3.connect("shop.db")


def get_order(order_id):
    db = get_db()
    cur = db.cursor()
    cur.execute(f"SELECT id, customer, total FROM orders WHERE id = {order_id}")
    row = cur.fetchone()
    db.close()
    return row


def list_orders(customer):
    db = get_db()
    cur = db.cursor()
    cur.execute("SELECT id, total FROM orders WHERE customer = ?", (customer,))
    rows = cur.fetchall()
    db.close()
    return rows

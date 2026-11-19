from dataclasses import dataclass


@dataclass
class Item:
    name: str
    price: float
    quantity: int


def total(items, discount_percent=0):
    subtotal = sum(i.price * i.quantity for i in items)
    discounted = subtotal - subtotal * discount_percent / 100
    return round(discounted, 2)


def apply_coupon(items, code):
    if code == "HALF":
        return total(items, 50)
    if code == "TENOFF":
        return total(items) - 10
    return total(items)

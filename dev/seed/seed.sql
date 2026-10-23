-- Local development seed data. NOT a migration: it never runs in CI, staging
-- or production, and it isn't part of any image. Run it with
--   docker compose run --rm seed
-- (see docs/database.md#seed-data).
--
-- The compose service runs this file with psql --single-transaction and
-- ON_ERROR_STOP=1, so it applies completely or not at all. Every insert is
-- guarded by ON CONFLICT DO NOTHING, so running it again changes nothing.

-- Users: one admin, two requesters. Addresses use the reserved example.com
-- domain. external_subject stays NULL until real authentication lands.
INSERT INTO users (email, display_name, role) VALUES
    ('admin@example.com',  'Avery Admin',     'admin'),
    ('robin@example.com',  'Robin Requester', 'requester'),
    ('casey@example.com',  'Casey Requester', 'requester')
ON CONFLICT ((lower(email))) DO NOTHING;

-- Products, and their opening stock.
--
-- products.total_quantity may only change together with a stock_adjustments
-- row, so each product that starts with stock gets one "opening stock"
-- adjustment by the seed admin (delta = quantity_after = total_quantity).
-- The adjustments are inserted only for products this run actually created
-- (the RETURNING rows of the products insert), so a re-run, or a SKU that
-- already exists locally, adds no adjustment and never touches existing stock.
-- Products that start at 0 get no adjustment (delta can't be 0).
--
-- SKUs are exactly 6 digits, the team's SKU format.
WITH seed (sku, name, description, category, total_quantity) AS (
    VALUES
        ('100101', 'Corrugated Shipping Box 12x12x12', 'Single-wall, 32 ECT, flat-packed',          'Packaging',     480),
        ('100102', 'Corrugated Shipping Box 18x12x6',  'Single-wall, 32 ECT, flat-packed',          'Packaging',      60),
        ('100103', 'Bubble Wrap Roll 12in x 175ft',    '3/16in bubble, perforated every 12in',      'Packaging',      35),
        ('100104', 'Packing Tape 48mm x 100m',         'Clear acrylic, case of 36 rolls',           'Packaging',      22),
        ('100105', 'Stretch Wrap 18in x 1500ft',       '80 gauge cast film',                        'Packaging',       0),
        ('200201', 'Standard Wooden Pallet 48x40',     'GMA grade A, four-way entry',               'Pallets',       140),
        ('200202', 'Plastic Pallet 48x40',             'Nestable, 2,500 lb dynamic load',           'Pallets',        18),
        ('300301', 'Hand Pallet Truck 5500lb',         '27x48in forks, manual hydraulic',           'Handling',        6),
        ('300302', 'Two-Wheel Hand Truck',             'Steel frame, pneumatic tires, 600 lb',      'Handling',        9),
        ('300303', 'Platform Cart 24x48',              'Steel deck, 1,200 lb capacity',             'Handling',        4),
        ('400401', 'Steel Shelving Unit 72x36x18',     'Five shelves, boltless',                    'Storage',        12),
        ('400402', 'Stackable Bin 15x8x7',             'Polypropylene, blue, label holder',         'Storage',       250),
        ('400403', 'Pallet Rack Beam 96in',            'Step beam, 5,000 lb per pair',              'Storage',        48),
        ('500501', 'Thermal Shipping Label 4x6',       'Direct thermal, 250 labels per roll',       'Labels',         90),
        ('500502', 'Barcode Scanner, Cordless',        '2D imager, USB charging cradle',            'Electronics',     7),
        ('500503', 'Thermal Label Printer 4in',        '203 dpi, USB and Ethernet',                 'Electronics',     2),
        ('600601', 'Safety Vest, Hi-Vis Class 2',      'ANSI/ISEA 107, size L',                     'Safety',         55),
        ('600602', 'Steel Toe Boots, Size 10',         'ASTM F2413 rated',                          'Safety',          0),
        ('600603', 'Nitrile Work Gloves, Box of 100',  '5 mil, size L',                             'Safety',         28),
        ('700701', 'Box Cutter, Retractable',          'Auto-retracting safety blade',              NULL,             64)
),
inserted AS (
    INSERT INTO products (sku, name, description, category, total_quantity)
    SELECT sku, name, description, category, total_quantity FROM seed
    ON CONFLICT (sku) DO NOTHING
    RETURNING id, total_quantity
)
INSERT INTO stock_adjustments (product_id, delta, quantity_after, reason, actor_id)
SELECT i.id, i.total_quantity, i.total_quantity, 'Opening stock (local seed data)', admin.id
FROM inserted i
CROSS JOIN (SELECT id FROM users WHERE lower(email) = 'admin@example.com') admin
WHERE i.total_quantity > 0;

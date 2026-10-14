import os
import math
from PIL import Image, ImageDraw, ImageFilter

def cubic_bezier(p0, p1, p2, p3, steps=40):
    pts = []
    for i in range(steps + 1):
        t = i / steps
        u = 1.0 - t
        x = u**3 * p0[0] + 3 * u**2 * t * p1[0] + 3 * u * t**2 * p2[0] + t**3 * p3[0]
        y = u**3 * p0[1] + 3 * u**2 * t * p1[1] + 3 * u * t**2 * p2[1] + t**3 * p3[1]
        pts.append((x, y))
    return pts

def render_master_icon(size=1080, is_round=False):
    # Master image at 1080x1080
    img = Image.new('RGBA', (size, size), (0, 0, 0, 0))
    scale = size / 108.0

    # 1. Background Mask
    mask = Image.new('L', (size, size), 0)
    draw_mask = ImageDraw.Draw(mask)
    if is_round:
        draw_mask.ellipse([(0, 0), (size, size)], fill=255)
    else:
        # Modern smooth squircle / rounded rect (radius ~ 22% of dimension)
        corner_r = int(size * 0.22)
        draw_mask.rounded_rectangle([(0, 0), (size, size)], radius=corner_r, fill=255)

    # 2. Background Gradient (Deep Telegram Space Navy)
    bg = Image.new('RGBA', (size, size), (0, 0, 0, 0))
    draw_bg = ImageDraw.Draw(bg)
    top_color = (26, 38, 53)     # #1A2635
    bottom_color = (13, 20, 30)  # #0D141E
    for y in range(size):
        t = y / (size - 1)
        r = int(top_color[0] + (bottom_color[0] - top_color[0]) * t)
        g = int(top_color[1] + (bottom_color[1] - top_color[1]) * t)
        b = int(top_color[2] + (bottom_color[2] - top_color[2]) * t)
        draw_bg.line([(0, y), (size, y)], fill=(r, g, b, 255))

    # Add soft ambient radial glow behind the shield
    glow_ambient = Image.new('RGBA', (size, size), (0, 0, 0, 0))
    draw_glow_amb = ImageDraw.Draw(glow_ambient)
    center = (int(size * 0.5), int(size * 0.5))
    glow_r = int(size * 0.38)
    draw_glow_amb.ellipse([
        (center[0] - glow_r, center[1] - glow_r),
        (center[0] + glow_r, center[1] + glow_r)
    ], fill=(42, 171, 238, 38))
    glow_ambient = glow_ambient.filter(ImageFilter.GaussianBlur(int(size * 0.12)))

    # Composite background with mask
    bg_composite = Image.alpha_composite(bg, glow_ambient)
    img.paste(bg_composite, (0, 0), mask)

    # 3. Outer Shield Glow
    glow_img = Image.new('RGBA', (size, size), (0, 0, 0, 0))
    draw_glow = ImageDraw.Draw(glow_img)
    g1 = cubic_bezier((54*scale, 25*scale), (64*scale, 29*scale), (73*scale, 31*scale), (78.5*scale, 33*scale))
    g2 = cubic_bezier((78.5*scale, 33*scale), (78.5*scale, 53.5*scale), (71.5*scale, 71*scale), (54*scale, 82.5*scale))
    g3 = cubic_bezier((54*scale, 82.5*scale), (36.5*scale, 71*scale), (29.5*scale, 53.5*scale), (29.5*scale, 33*scale))
    g4 = cubic_bezier((29.5*scale, 33*scale), (35*scale, 31*scale), (44*scale, 29*scale), (54*scale, 25*scale))
    glow_pts = g1 + g2 + g3 + g4
    draw_glow.polygon(glow_pts, fill=(42, 171, 238, 110))
    glow_img = glow_img.filter(ImageFilter.GaussianBlur(int(scale * 2.2)))
    img = Image.alpha_composite(img, glow_img)

    # 4. Shield Geometry & Gradient
    s1 = cubic_bezier((54*scale, 27*scale), (63*scale, 31*scale), (71*scale, 33*scale), (76*scale, 35*scale))
    s2 = cubic_bezier((76*scale, 35*scale), (76*scale, 52*scale), (70*scale, 68*scale), (54*scale, 80*scale))
    s3 = cubic_bezier((54*scale, 80*scale), (38*scale, 68*scale), (32*scale, 52*scale), (32*scale, 35*scale))
    s4 = cubic_bezier((32*scale, 35*scale), (37*scale, 33*scale), (45*scale, 31*scale), (54*scale, 27*scale))
    shield_pts = s1 + s2 + s3 + s4

    shield_mask = Image.new('L', (size, size), 0)
    draw_sm = ImageDraw.Draw(shield_mask)
    draw_sm.polygon(shield_pts, fill=255)

    shield_grad = Image.new('RGBA', (size, size), (0, 0, 0, 0))
    draw_sg = ImageDraw.Draw(shield_grad)
    y_start = int(27 * scale)
    y_end = int(80 * scale)
    
    # 3-stop gradient: Top Sky Cyan -> Telegram Signature Blue -> Deep Cobalt
    c_top = (56, 185, 255)      # #38B9FF
    c_mid = (42, 171, 238)      # #2AABEE
    c_bot = (21, 101, 192)      # #1565C0
    
    for y in range(y_start, y_end + 1):
        frac = (y - y_start) / max(1, (y_end - y_start))
        if frac < 0.5:
            sub = frac * 2.0
            r = int(c_top[0] + (c_mid[0] - c_top[0]) * sub)
            g = int(c_top[1] + (c_mid[1] - c_top[1]) * sub)
            b = int(c_top[2] + (c_mid[2] - c_top[2]) * sub)
        else:
            sub = (frac - 0.5) * 2.0
            r = int(c_mid[0] + (c_bot[0] - c_mid[0]) * sub)
            g = int(c_mid[1] + (c_bot[1] - c_mid[1]) * sub)
            b = int(c_mid[2] + (c_bot[2] - c_mid[2]) * sub)
        draw_sg.line([(0, y), (size, y)], fill=(r, g, b, 255))

    img.paste(shield_grad, (0, 0), shield_mask)

    # 5. Inner Shield Rim (Specular Highlight)
    r1 = cubic_bezier((54*scale, 29.5*scale), (62*scale, 33*scale), (69*scale, 35*scale), (73.5*scale, 36.8*scale))
    r2 = cubic_bezier((73.5*scale, 36.8*scale), (73.5*scale, 50.5*scale), (68*scale, 65*scale), (54*scale, 76.5*scale))
    r3 = cubic_bezier((54*scale, 76.5*scale), (40*scale, 65*scale), (34.5*scale, 50.5*scale), (34.5*scale, 36.8*scale))
    r4 = cubic_bezier((34.5*scale, 36.8*scale), (39*scale, 35*scale), (46*scale, 33*scale), (54*scale, 29.5*scale))
    rim_pts = r1 + r2 + r3 + r4
    draw_rim = ImageDraw.Draw(img)
    rim_width = max(1, int(scale * 1.2))
    draw_rim.line(rim_pts + [rim_pts[0]], fill=(255, 255, 255, 90), width=rim_width)

    # 6. Telegram Paper Plane
    # Drop shadow under plane
    shadow_img = Image.new('RGBA', (size, size), (0, 0, 0, 0))
    draw_shadow = ImageDraw.Draw(shadow_img)

    plane_scale = 1.6 * scale
    tx = 34.8 * scale
    ty = 34.0 * scale

    p_wing = [
        (9.78, 18.65), (10.06, 14.42), (17.74, 7.50), (17.22, 7.31),
        (7.74, 13.30), (3.64, 12.00), (3.84, 10.70), (19.81, 4.54),
        (20.96, 5.84), (18.24, 18.65), (16.74, 19.36), (12.60, 16.30),
        (10.61, 18.23)
    ]
    p_wing_px = [(x * plane_scale + tx, y * plane_scale + ty) for x, y in p_wing]
    p_shadow_px = [(x * plane_scale + tx, y * plane_scale + ty + int(scale * 0.8)) for x, y in p_wing]

    draw_shadow.polygon(p_shadow_px, fill=(0, 20, 45, 60))
    shadow_img = shadow_img.filter(ImageFilter.GaussianBlur(int(scale * 1.0)))
    img = Image.alpha_composite(img, shadow_img)

    # Draw pure crisp white wing
    draw_plane = ImageDraw.Draw(img)
    draw_plane.polygon(p_wing_px, fill=(255, 255, 255, 255))

    # Draw 3D crease
    p_crease = [(10.06, 14.42), (12.60, 16.30), (10.61, 18.23)]
    p_crease_px = [(x * plane_scale + tx, y * plane_scale + ty) for x, y in p_crease]
    draw_plane.polygon(p_crease_px, fill=(188, 215, 239, 255))

    return img

if __name__ == '__main__':
    print("Generating master icons...")
    master_square = render_master_icon(size=1080, is_round=False)
    master_round = render_master_icon(size=1080, is_round=True)

    densities = {
        'mipmap-mdpi': 48,
        'mipmap-hdpi': 72,
        'mipmap-xhdpi': 96,
        'mipmap-xxhdpi': 144,
        'mipmap-xxxhdpi': 192,
    }

    script_dir = os.path.dirname(os.path.abspath(__file__))
    project_root = os.path.abspath(os.path.join(script_dir, ".."))
    base_dir = os.path.join(project_root, "android", "app", "src", "main", "res")
    preview_path = os.path.join(project_root, "android", "ic_launcher-512.png")
    
    for folder, dim in densities.items():
        out_dir = os.path.join(base_dir, folder)
        os.makedirs(out_dir, exist_ok=True)
        
        sq = master_square.resize((dim, dim), Image.Resampling.LANCZOS)
        sq.save(os.path.join(out_dir, "ic_launcher.png"), "PNG")
        
        rd = master_round.resize((dim, dim), Image.Resampling.LANCZOS)
        rd.save(os.path.join(out_dir, "ic_launcher_round.png"), "PNG")
        print(f"Generated {folder}: {dim}x{dim}")

    # Also save 512x512 preview in base android folder for Play Store / repo preview
    preview_sq = master_square.resize((512, 512), Image.Resampling.LANCZOS)
    preview_sq.save(preview_path, "PNG")
    print(f"Saved 512x512 preview to {preview_path}")
